package modelgateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// RevokeRequest is the wire shape of POST /__gateway/revoke. It lives here
// (not in cmd/model-gateway) so both sides of the wire build/parse the exact
// same JSON without a new import cycle: cmd/model-gateway already imports
// this package for GatewayClaims/MintToken, so its `revoke --jti` subcommand
// marshals this same struct rather than hand-building a request body that
// could drift from what the server decodes (see TestRevokeRequest_JSONShape,
// the golden shared by both).
type RevokeRequest struct {
	JTI string `json:"jti"`
	// ExpiresAt is RFC3339, and optional: empty (or omitted) means the
	// revocation never expires, mirroring MemRevocations' own treatment of a
	// zero time.Time (revocation.go). This is the safe default for a caller
	// that doesn't know the token's real expiry — e.g. cmd/model-gateway's
	// `revoke --jti` without --expires-at — since a wrong-but-plausible guess
	// (like "now+24h") would let MemRevocations' sweep silently un-revoke a
	// token whose real TTL runs longer, exactly the recipe-box case this
	// feature exists for (#1820 review).
	ExpiresAt string `json:"expires_at,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// revoker is the subset of a revocation store the admin endpoint needs to
// actually revoke, as opposed to RevocationChecker's read-only IsRevoked.
// Declared locally — like RevocationChecker in revocation.go — so this
// package keeps no dependency on internal/auth or internal/runlease. Both
// MemRevocations and the daemon's internal/auth.PgRevocationStore already
// implement this exact method shape (auth.RevocationStore.Revoke /
// runlease.Revoker), so either can sit behind Config.Revocations and be
// driven by this endpoint with no adapter.
type revoker interface {
	Revoke(ctx context.Context, jti string, expiresAt time.Time, reason string) error
}
