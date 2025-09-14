package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var (
	tokenUsername   string
	tokenRoles      []string
	tokenScopes     []string // Phase 1.7 — least-privilege scope list
	tokenType       string   // Phase 1.6 — "access" (default) or "refresh"
	tokenExpiry     string
	tokenSecretFlag string
	tokenSecretFile string
	tokenRaw        bool

	// `token revoke` flags
	revokeJTI       string
	revokeReason    string
	revokeExpiresAt string

	// `token refresh` flags (Phase 1.6 part B)
	refreshTokenIn   string
	refreshTokenFile string

	// `token delegate` flags (containarium-cloud#1427)
	delegateSubject   string
	delegateScopes    []string
	delegateRoles     []string
	delegateExpiry    string
	delegateRawOutput bool

	// `token list-revoked` flags
	listRevokedLimit          int32
	listRevokedIncludeExpired bool
	listRevokedJTIPrefix      string
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage API tokens for REST API authentication",
	Long: `Generate and manage JWT tokens for REST API authentication.

Tokens are required to authenticate REST API requests. Each token contains:
  - Username: Identity of the user
  - Roles: Permissions/roles assigned to the user
  - Expiry: When the token expires (optional)

Tokens are signed with a secret key that must match the daemon's JWT secret.`,
}

var tokenGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate a new API token",
	Long: `Generate a new JWT token for REST API authentication.

The generated token can be used in the Authorization header:
  Authorization: Bearer <token>

Token Expiry:
  - Use standard duration format: 24h, 168h, 720h, etc.
  - Non-expiring tokens are not supported: 0, empty, or a value above the
    daemon's cap (default 720h / 30 days) is an error, not a silent clamp
  - Recommended expiry times:
    - Admin tokens: 720h (30 days)
    - User tokens: 168h (7 days)
    - Unattended jobs: mint a short-lived token per run from the signing
      secret on the daemon host (see docs/DB-BACKUP-OPERATIONS.md)
    - Development tokens: 24h

Security:
  - Store tokens securely
  - Never commit tokens to version control
  - Rotate tokens periodically
  - Use short expiry times when possible`,
	Example: `  # Generate token for admin user (30 days expiry)
  containarium token generate --username admin --roles admin --expiry 720h --secret <JWT_SECRET>

  # Generate token from secret file
  containarium token generate --username alice --secret-file /etc/containarium/jwt.secret

  # Generate a short-lived token for a scheduled job (mint per run)
  containarium token generate --username backup-scheduler --roles admin --expiry 2h --secret-file /etc/containarium/jwt.secret --raw

  # Generate development token (24h expiry)
  containarium token generate --username dev --roles user,developer --expiry 24h --secret dev-secret`,
	RunE: runTokenGenerate,
}

// tokenRevokeCmd implements `containarium token revoke` — the
// admin-facing CLI for the JWT revocation list landed in
// Phase 1.2 (PR #248). It talks to the daemon over the
// canonical HTTP gateway; the daemon does the actual write
// and enforces the admin role.
//
// Operators get the jti either from the audit trail
// (every authenticated request logs `jti=<id>`) or by
// decoding the JWT they want to kill.
var tokenRevokeCmd = &cobra.Command{
	Use:   "revoke",
	Short: "Revoke a JWT by its jti (admin-only)",
	Long: `Add a JWT's jti to the daemon's revocation list.

The token will be rejected on the next request that names it.
Idempotent — repeated revokes preserve the original reason.

Admin-only on the server side. The daemon must be reachable
via --server, and --token must name an admin JWT.

Locate the jti to revoke either from the audit log (every
authenticated request logs jti=<id>) or by base64-decoding
the JWT payload (the 'jti' claim).`,
	Example: `  # Revoke a leaked token
  containarium token revoke \
    --jti AbCdEfGh... \
    --reason "leaked_to_public_gist_2026_05" \
    --server https://<cluster>.example.com \
    --token $ADMIN_TOKEN

  # Revoke with explicit cleanup horizon (the token's own exp)
  containarium token revoke \
    --jti AbCdEfGh... \
    --expires-at 2026-06-19T12:34:56Z \
    --server https://<cluster>.example.com \
    --token $ADMIN_TOKEN`,
	RunE: runTokenRevoke,
}

// tokenRefreshCmd implements `containarium token refresh` —
// Phase 1.6 part B. Exchanges a long-lived refresh token
// for a new short-lived access token (and a new refresh
// token, since refresh tokens are single-use). The old
// refresh token is revoked server-side on success.
var tokenRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Exchange a refresh token for a new (access, refresh) pair",
	Long: `Trade a long-lived refresh token for a new short-lived access
token. The server also issues a fresh refresh token because
refresh tokens are single-use (rotation) — save the new one
for the next exchange.

The refresh-exchange endpoint is unauthenticated: the
refresh-token body IS the credential. Do not pass --token.

Use the access token in subsequent API calls:
  Authorization: Bearer <access_token>`,
	Example: `  # From an environment variable
  containarium token refresh \
    --refresh-token "$REFRESH" \
    --server https://<cluster>.example.com

  # From a file (mode 0600 recommended)
  containarium token refresh \
    --refresh-token-file ~/.containarium/refresh \
    --server https://<cluster>.example.com`,
	RunE: runTokenRefresh,
}

// tokenListRevokedCmd implements `containarium token
// list-revoked` — admin enumeration of the revocation
// list. Confirms a revoke landed; surfaces what got killed
// during an incident window.
var tokenListRevokedCmd = &cobra.Command{
	Use:   "list-revoked",
	Short: "List active JWT revocations (admin-only)",
	Long: `Enumerate the revocation list. By default returns only
revocations whose token exp is still in the future ("active
kill-switches"); pass --include-expired for forensic queries
that need the full history.

Admin role + tokens:write scope required.`,
	Example: `  # Confirm the last revoke landed
  containarium token list-revoked --server $S --token $T

  # All revocations for a partial jti you remember
  containarium token list-revoked --jti-prefix "AbCd" --server $S --token $T

  # Forensic: every row, including expired
  containarium token list-revoked --include-expired --limit 500 --server $S --token $T`,
	RunE: runTokenListRevoked,
}

// tokenDelegateCmd implements `containarium token delegate`
// (containarium-cloud#1427) — trade your own credential for one that acts
// FOR someone else.
var tokenDelegateCmd = &cobra.Command{
	Use:   "delegate",
	Short: "Exchange your token for one acting on behalf of another subject",
	Long: `Trade the credential you already hold for a short-lived token minted
for another subject, with your identity recorded as the actor.

This exists for a service that fronts the API for end users. Such a
service presents its own service account on every call, so audit rows
name the service rather than the person, and a scope check against the
service account bounds nothing. Rather than hand that service a signing
key — which would let it mint any identity at all — it asks the daemon
to mint, and the daemon records the delegation itself.

Two limits are enforced server-side and cannot be argued with:

  - The granted scopes are the intersection of yours with those you
    request. This exchange can only narrow authority, never widen it.
  - The actor is taken from your authenticated token, never from the
    request. You cannot name someone else as the delegator.

Requires the tokens:delegate scope — deliberately distinct from
tokens:write, because managing your own tokens and acting as another
person are different capabilities.`,
	Example: `  # Act for a user, inheriting every scope you hold
  containarium token delegate --subject alice@example.com \
    --server $S --token $T

  # Narrow it further than your own token, and shorten the life
  containarium token delegate --subject alice@example.com \
    --scopes containers:read --expiry 5m \
    --server $S --token $T

  # For scripting: emit only the token
  DELEGATED=$(containarium token delegate --subject alice@example.com \
    --raw --server $S --token $T)`,
	RunE: runTokenDelegate,
}

// tokenUnscopedReportCmd implements `containarium token
// unscoped-report` (#1679) — the measurement to check before
// arming CONTAINARIUM_STRICT_SCOPES.
var tokenUnscopedReportCmd = &cobra.Command{
	Use:   "unscoped-report",
	Short: "Report how many recent calls used a token with no scopes claim (admin-only)",
	Long: `Reports the in-process count of calls whose token carried no scopes
claim at all — the population CONTAINARIUM_STRICT_SCOPES would start
rejecting. Check this before arming strict mode so the rollout is a
measured decision, not a gamble. Resets on daemon restart.

Admin role or the tokens:read scope required.`,
	Example: `  containarium token unscoped-report --server $S --token $T`,
	RunE:    runTokenUnscopedReport,
}

func init() {
	rootCmd.AddCommand(tokenCmd)
	tokenCmd.AddCommand(tokenGenerateCmd)
	tokenCmd.AddCommand(tokenRevokeCmd)
	tokenCmd.AddCommand(tokenRefreshCmd)
	tokenCmd.AddCommand(tokenListRevokedCmd)
	tokenCmd.AddCommand(tokenUnscopedReportCmd)
	tokenCmd.AddCommand(tokenDelegateCmd)

	tokenDelegateCmd.Flags().StringVar(&delegateSubject, "subject", "", "Subject to act on behalf of (required)")
	_ = tokenDelegateCmd.MarkFlagRequired("subject")
	tokenDelegateCmd.Flags().StringSliceVar(&delegateScopes, "scopes", nil, "Scopes to request (comma-separated). Omit to inherit your own; the server intersects either way, so this can only narrow.")
	tokenDelegateCmd.Flags().StringSliceVar(&delegateRoles, "roles", nil, "Roles to request (comma-separated, e.g. admin). Intersected with your own. UNLIKE --scopes, omitting this grants NO roles rather than inheriting yours: a role-less token can act for its subject but cannot reach admin-gated RPCs or other tenants.")
	tokenDelegateCmd.Flags().StringVar(&delegateExpiry, "expiry", "", "Lifetime of the delegated token (e.g. 5m, 1h). Empty = the daemon's access-token default.")
	tokenDelegateCmd.Flags().BoolVar(&delegateRawOutput, "raw", false, "Output only the raw token (for scripting)")

	tokenRefreshCmd.Flags().StringVar(&refreshTokenIn, "refresh-token", "", "Refresh token to exchange (mutually exclusive with --refresh-token-file)")
	tokenRefreshCmd.Flags().StringVar(&refreshTokenFile, "refresh-token-file", "", "Path to file containing the refresh token (mode 0600 recommended)")

	tokenRevokeCmd.Flags().StringVar(&revokeJTI, "jti", "", "jti claim of the token to revoke (required)")
	_ = tokenRevokeCmd.MarkFlagRequired("jti")
	tokenRevokeCmd.Flags().StringVar(&revokeReason, "reason", "", "Free-form reason recorded for forensics (default: 'operator_revoke')")
	tokenRevokeCmd.Flags().StringVar(&revokeExpiresAt, "expires-at", "", "Token's own exp claim in RFC3339 (controls cleanup horizon; default: daemon max lifetime)")

	tokenListRevokedCmd.Flags().Int32Var(&listRevokedLimit, "limit", 100, "Max rows to return (server caps at 1000)")
	tokenListRevokedCmd.Flags().BoolVar(&listRevokedIncludeExpired, "include-expired", false, "Include rows whose token exp is already past (default: active only)")
	tokenListRevokedCmd.Flags().StringVar(&listRevokedJTIPrefix, "jti-prefix", "", "Narrow results to jtis with this prefix (default: all)")

	// Required flags
	tokenGenerateCmd.Flags().StringVar(&tokenUsername, "username", "", "Username for the token (required)")
	_ = tokenGenerateCmd.MarkFlagRequired("username")

	// Secret flags (one required)
	tokenGenerateCmd.Flags().StringVar(&tokenSecretFlag, "secret", "", "JWT secret key")
	tokenGenerateCmd.Flags().StringVar(&tokenSecretFile, "secret-file", "", "Path to file containing JWT secret key")

	// Optional flags
	tokenGenerateCmd.Flags().StringSliceVar(&tokenRoles, "roles", []string{"user"}, "Roles for the token (comma-separated)")
	tokenGenerateCmd.Flags().StringSliceVar(&tokenScopes, "scopes", nil, "Phase 1.7: least-privilege scopes (comma-separated; e.g. containers:read,secrets:read). Omit for an unrestricted token; pass '*' for the wildcard.")
	tokenGenerateCmd.Flags().StringVar(&tokenType, "token-type", "", "Phase 1.6: 'access' (short-lived API token) or 'refresh' (long-lived token for exchange). Empty = legacy issuance (still acts as access on the API surface).")
	tokenGenerateCmd.Flags().StringVar(&tokenExpiry, "expiry", "24h", "Token expiry duration (e.g., 24h, 168h, 720h). Must be positive and not exceed the daemon cap (default 720h); non-expiring tokens are not supported")
	tokenGenerateCmd.Flags().BoolVar(&tokenRaw, "raw", false, "Output only the raw token (for scripting)")
}

// resolveTokenExpiry parses --expiry and rejects anything the token manager
// would otherwise silently clamp: zero/empty ("no expiry"), negative, or
// above max. Non-expiring tokens do not exist (#2592).
func resolveTokenExpiry(raw string, max time.Duration) (time.Duration, error) {
	if raw == "" || raw == "0" {
		return 0, fmt.Errorf("non-expiring tokens are not supported; maximum is %gh", max.Hours())
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid expiry duration '%s': %w\nExamples: 24h, 168h, 720h", raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("--expiry must be positive; maximum is %gh", max.Hours())
	}
	if d > max {
		return 0, fmt.Errorf("--expiry %s is too long; maximum is %gh", raw, max.Hours())
	}
	return d, nil
}

func runTokenGenerate(cmd *cobra.Command, args []string) error {
	// Load JWT secret
	secret := tokenSecretFlag
	if tokenSecretFile != "" {
		secretBytes, err := os.ReadFile(tokenSecretFile)
		if err != nil {
			return fmt.Errorf("failed to read JWT secret file: %w", err)
		}
		secret = strings.TrimSpace(string(secretBytes))
