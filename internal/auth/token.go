package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/footprintai/containarium/internal/config"
	"github.com/footprintai/containarium/internal/tokenid"
	"github.com/golang-jwt/jwt/v5"
)

// MintedID is what an issuer must keep to revoke a token it issued: the jti
// a mint stamped and the expiry it signed. A type alias (not a wrapper
// struct) onto internal/tokenid.MintedID so internal/modelgateway — which
// imports no other internal/ package — can share the exact same type
// without importing internal/auth. See internal/tokenid's package doc.
type MintedID = tokenid.MintedID

// DefaultMaxTokenExpiry is the default maximum token expiry (30 days)
const DefaultMaxTokenExpiry = 30 * 24 * time.Hour

// MinSecretKeyLen is the smallest acceptable JWT signing key.
// HMAC-SHA256 expects a key with at least 32 bytes of entropy for
// the security level it claims; weaker keys are brute-forceable
// offline within practical compute budgets. Tracks audit finding
// A-MED-2.
const MinSecretKeyLen = 32

// DefaultAudience is the audience claim Containarium-issued tokens
// carry by default. Validators reject tokens whose `aud` doesn't
// include this string, so a token minted for an unrelated service
// (or by a future tool that shares the same signing key) can't be
// replayed against the daemon. Tracks audit finding A-HIGH-1.
const DefaultAudience = "containarium-api"

// Claims represents the JWT claims for authentication.
//
// Phase 1.7 — `Scopes` is the OAuth2-style scope list for
// least-privilege tokens (typically agent-facing tokens).
// `omitempty` keeps pre-1.7 tokens identical on the wire;
// HasScope treats a nil/missing scopes array as "no
// restriction" for backwards compat.
//
// Phase 1.6 — `TokenType` (claim `tt`) distinguishes access
// tokens (used for API auth) from refresh tokens (used only
// to exchange for new access tokens). `omitempty` keeps
// pre-1.6 tokens identical on the wire; an empty / missing
// tt is interpreted as TokenTypeAccess (backwards compat).
// API auth (HTTP middleware) calls ValidateAccessToken,
// which refuses any token whose tt is explicitly
// "refresh" — so a stolen refresh token can't make API
// calls even though it has a valid signature.
type Claims struct {
	Username  string   `json:"username"`
	Roles     []string `json:"roles"`
	Scopes    []string `json:"scopes,omitempty"`
	TokenType string   `json:"tt,omitempty"`
	// Act (#1677) is the RFC 8693 delegation claim: who this token was
	// minted on behalf of. `omitempty` keeps every token minted for its own
	// subject — which is every pre-1677 token, and every token minted
	// today for a human/CLI/system caller — identical on the wire. Absence
	// is valid and reported as unattributed, never rejected; see
	// GenerateDelegatedToken and ActFromGRPCContext.
	Act *Actor `json:"act,omitempty"`
	// RunID (#1815) binds a token to one skill run, so the issuer can
	// revoke exactly the credentials it minted for that run without
	// affecting any other token issued for the same subject. `omitempty`
	// keeps every mint path that doesn't pass a run id (which is every
	// pre-#1815 call site) identical on the wire.
	RunID string `json:"run_id,omitempty"`
	// TrackerConn (#1922) binds a run-scoped token to exactly one named
	// tracker connection, minted at RunAgentSkill time from
	// RunAgentSkillRequest.tracker_connection after validating it against
	// the caller's tenant. The tracker verb RPCs reject a request naming
	// a different connection than this claim — the same anti-forgery
	// shape as Act: derived from the verified token, never trusted from
	// a request field. `omitempty` keeps every token minted without a
	// tracker connection (every pre-#1922 call site, and every run not
	// bound to one) identical on the wire. Empty means "no binding" —
	// an operator token (no run_id either) may name any connection in
	// its tenant.
	TrackerConn string `json:"tracker_conn,omitempty"`
	// RunTenant (#2268) names the tenant a run-scoped token acts for: the
	// subject that dispatched the run, whose tracker connection
	// TrackerConn was validated against. A run token's own Username is the
	// BOX (agent-<skill-id>, no roles), so AuthorizeTenant can never match
	// it to the tenant the in-box tracker_* tools name — this claim is
	// what AuthorizeTrackerTenant matches instead, and only on a token that
	// also carries RunID. Same anti-forgery shape as Act and TrackerConn:
	// derived from the dispatching caller's verified token at mint time,
	// never from a request field. `omitempty` keeps every token minted
	// outside the run path identical on the wire; it is never copied onto
	// a token derived from a run token (ExchangeDelegatedToken mints
	// without it), so it cannot be laundered onto a non-run credential.
	RunTenant string `json:"run_tenant,omitempty"`
	// FamilyID identifies a refresh token's rotation chain: every token
	// minted by successive RefreshToken exchanges starting from one
	// original login shares the same value. It exists so reuse of an
	// already-rotated refresh token — the signal a stolen-and-replayed
	// token produces — can revoke every token descended from it, not
	// just the one jti that was replayed. `omitempty` keeps access
	// tokens and every pre-family-tracking refresh token (where it
	// falls back to the token's own jti) unaffected on the wire.
	FamilyID string `json:"fam,omitempty"`
	jwt.RegisteredClaims
}

// Token-type constants for the `tt` claim.
const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

// DefaultRefreshTokenExpiry is the cap for refresh-token
// lifetime when a caller passes zero. Long enough for a
// CLI / agent session, short enough that a leaked token
// has bounded blast radius. Capped at the same
// maxTokenExpiry as access tokens — operators can shorten
// either via CONTAINARIUM_MAX_TOKEN_EXPIRY_HOURS.
const DefaultRefreshTokenExpiry = 30 * 24 * time.Hour

// DefaultAccessTokenExpiry is the default for access tokens
// minted via GenerateAccessToken when caller passes 0.
// Short by design — that's the whole point of Phase 1.6;
// the refresh token does the long-lived work.
const DefaultAccessTokenExpiry = 15 * time.Minute

// TokenManager handles JWT token generation and validation
type TokenManager struct {
	secretKey      []byte
	issuer         string
	audience       string
	maxTokenExpiry time.Duration

	// Phase 1.2 — optional revocation list. nil = check
	// disabled (tests, minimal deployments). Production
	// wires a Postgres-backed PgRevocationStore here.
	revocationStore RevocationStore
}

// SetRevocationStore enables the revocation-list check on
// ValidateToken. Pass nil to disable. Typically wired once
// at daemon startup after the Postgres pool is ready.
func (tm *TokenManager) SetRevocationStore(store RevocationStore) {
	tm.revocationStore = store
}

// NewTokenManager creates a new token manager. Returns an error if
// the secret key is shorter than MinSecretKeyLen — fail-closed
// rather than silently accepting weak crypto (audit finding
// A-MED-2).
//
// The default audience is DefaultAudience; override via
// CONTAINARIUM_JWT_AUDIENCE for cross-tenant token issuance.
func NewTokenManager(secretKey string, issuer string) (*TokenManager, error) {
	if len(secretKey) < MinSecretKeyLen {
		return nil, fmt.Errorf("JWT secret is %d bytes, want >=%d (HMAC-SHA256 minimum); generate with `openssl rand -base64 48`", len(secretKey), MinSecretKeyLen)
	}

	maxExpiry := DefaultMaxTokenExpiry

	// Allow override via environment variable (in hours)
	if envMaxExpiry := os.Getenv("CONTAINARIUM_MAX_TOKEN_EXPIRY_HOURS"); envMaxExpiry != "" {
		if hours, err := strconv.ParseInt(envMaxExpiry, 10, 64); err == nil && hours > 0 {
			maxExpiry = time.Duration(hours) * time.Hour
		}
	}

	audience := DefaultAudience
	if env := os.Getenv(config.EnvJWTAudience); env != "" {
		audience = env
	}

	return &TokenManager{
		secretKey:      []byte(secretKey),
		issuer:         issuer,
		audience:       audience,
		maxTokenExpiry: maxExpiry,
	}, nil
}

// MaxTokenExpiry returns the effective maximum token lifetime (default 30d,
// or CONTAINARIUM_MAX_TOKEN_EXPIRY_HOURS). Callers that accept an operator-
// supplied lifetime use it to reject out-of-range values rather than relying
// on the silent clamp in generate (#2592).
func (tm *TokenManager) MaxTokenExpiry() time.Duration { return tm.maxTokenExpiry }

// GenerateToken creates a JWT token for a user.
//
// SECURITY FIX: Non-expiring tokens are no longer allowed.
// Maximum expiry is enforced (default: 30 days, configurable
// via CONTAINARIUM_MAX_TOKEN_EXPIRY_HOURS).
//
// Phase 1.2: every token now carries a `jti` claim — a
// cryptographic-random 128-bit ID base64url-encoded. The
// jti is the key the revocation list operates on, so a
// token's lifetime can be cut short by writing one row.
//
// Phase 1.7: the variadic `scopes` parameter sets the
// least-privilege `scopes` claim. Pass nothing (or nil)
// for a no-restriction token — matches pre-1.7 behavior.
// Pass a list to mint a least-privilege token, e.g. for
// an LLM agent that should only see read APIs.
func (tm *TokenManager) GenerateToken(username string, roles []string, expiresIn time.Duration, scopes ...string) (string, error) {
	// Backwards-compatible shim. New code should call
	// GenerateAccessToken / GenerateRefreshToken directly.
	// Existing callers (CLI, daemon system tokens) keep
	// minting access tokens with their current call sites
	// because tt defaults to "" → access semantics.
	tok, _, err := tm.generate(username, roles, scopes, "", expiresIn, nil, RunBinding{}, "")
	return tok, err
}

// GenerateAccessToken mints a short-lived access token.
// Phase 1.6 — pass 0 for the daemon default
// (DefaultAccessTokenExpiry). Carries `tt: "access"`;
// ValidateAccessToken on the API surface enforces it.
func (tm *TokenManager) GenerateAccessToken(username string, roles []string, expiresIn time.Duration, scopes ...string) (string, error) {
	if expiresIn <= 0 {
		expiresIn = DefaultAccessTokenExpiry
	}
