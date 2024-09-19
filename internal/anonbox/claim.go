package anonbox

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/footprintai/containarium/pkg/core/box"
)

// Claim errors. The RPC layer maps them: AlreadyClaimed → AlreadyExists,
// Expired → FailedPrecondition, Invalid → PermissionDenied, NotFound →
// NotFound. Anything else is Internal.
var (
	ErrClaimAlreadyClaimed = errors.New("anonbox: box already claimed")
	ErrClaimExpired        = errors.New("anonbox: claim token expired")
	ErrClaimInvalid        = errors.New("anonbox: claim token invalid")
	ErrClaimNotFound       = errors.New("anonbox: box not found")
)

// LabelClaimedBy records the tenant that redeemed the token, beside
// LabelClaimedAt, so an operator can read both off the box.
const LabelClaimedBy = "anon.claimed_by"

// ClaimRequest is one redemption.
type ClaimRequest struct {
	Token          string
	Tenant         string   // new owner's username
	AuthorizedKeys []string // the tenant's keys to add; the claiming key stays
}

// ClaimResult is what a successful redemption changed.
type ClaimResult struct {
	BoxName string
	Tenant  string
	SSHUser string // unchanged: the box's own login, reached through the door
}

// tenantRe is the username shape every other tenant obeys.
var tenantRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Claim binds a box to a tenant via its single-use token (decision on
// #2199: the door path is kept — the box keeps its name and login and is
// still reached via `ssh new.<domain>` with the claiming key, now with no
// TTL; no jump account is created because normal routing could never
// reach a box named anon-<fp8>-container).
//
// Order matters: the claimed_at compare-and-set comes first so two
// concurrent redemptions cannot both convert the box; the conversion
// steps after it are idempotent, so a failure there leaves a box that is
// marked claimed and can be finished by an operator, never one that two
// tenants own.
func (m *Manager) Claim(ctx context.Context, req ClaimRequest) (*ClaimResult, error) {
	if !tenantRe.MatchString(req.Tenant) {
		return nil, InvalidRequestError{Err: fmt.Errorf("anonbox: invalid tenant %q", req.Tenant)}
	}
	if m.cfg.ClaimSecret == nil {
		return nil, errors.New("anonbox: claims are not enabled on this daemon (no claim secret)")
	}
	parsed, err := ParseClaimToken(req.Token)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrClaimInvalid, err)
	}
	st, err := m.findByName(ctx, parsed.BoxName)
	if err != nil {
		return nil, err
	}
	if st == nil || st.Labels[LabelFingerprint] == "" {
		return nil, ErrClaimNotFound
	}
	tok, err := VerifyClaimToken(m.cfg.ClaimSecret(parsed.BoxName), req.Token, m.cfg.Now())
	switch {
	case errors.Is(err, ErrTokenExpired):
		return nil, ErrClaimExpired
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrClaimInvalid, err)
	}
	if tok.TokenID != st.Labels[LabelClaimTokenID] || tok.FPHash != st.Labels[LabelFPHash] {
		return nil, fmt.Errorf("%w: token does not belong to this box", ErrClaimInvalid)
	}
