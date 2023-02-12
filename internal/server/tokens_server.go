package server

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Phase 1.2 follow-up — TokensService implementation. Pairs
// with the revocation list landed in PR #248 (`PgRevocationStore`).
//
// The RPC is admin-only. A tenant token can't kill another
// tenant's session — that's policy on the server side, not
// just convention. A future enhancement might allow a token
// to revoke its own jti regardless of role (self-logout
// flow), but the audit doc treats that as a separate
// follow-up; doing it now would mean two policies in one
// PR.

// TokensServer implements pb.TokensServiceServer.
type TokensServer struct {
	pb.UnimplementedTokensServiceServer

	tokenManager *auth.TokenManager
	store        auth.RevocationStore

	// maxLifetime is the cleanup horizon used when callers
	// don't supply expires_at. The value is the daemon's
	// configured max token lifetime — anything past that
	// can't authenticate anyway, so the revocation row is
	// safe to prune.
	maxLifetime time.Duration
}

// NewTokensServer wires the RPC handler. `store` is the
// shared revocation store (also referenced by tokenManager);
// passing it explicitly lets the handler insert with an
// operator-provided reason without round-tripping through
// the token manager.
func NewTokensServer(tm *auth.TokenManager, store auth.RevocationStore, maxLifetime time.Duration) *TokensServer {
	if maxLifetime <= 0 {
		maxLifetime = auth.DefaultMaxTokenExpiry
	}
	return &TokensServer{
		tokenManager: tm,
		store:        store,
		maxLifetime:  maxLifetime,
	}
}

// RevokeToken adds a jti to the revocation list. Admin-only.
//
// The cleanup horizon is, in order of preference:
//  1. expires_at from the request (caller knows the token's
//     real exp claim — best case),
//  2. now + daemon max token lifetime (worst-case fallback —
//     the row eventually prunes itself).
func (s *TokensServer) RevokeToken(ctx context.Context, req *pb.RevokeTokenRequest) (*pb.RevokeTokenResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeTokensWrite); err != nil {
		return nil, err
	}
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}
	if req.Jti == "" {
		return nil, status.Error(codes.InvalidArgument, "jti is required")
	}
	if s.store == nil {
		return nil, status.Error(codes.Unavailable, "revocation list is not configured on this daemon")
	}

	expiresAt := time.Now().Add(s.maxLifetime)
	if req.ExpiresAt != "" {
		// Accept RFC3339 (the canonical CLI format). Other
		// formats are caller-error and rejected — easier to
		// notice now than to discover after the row never
		// prunes.
		t, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "expires_at must be RFC3339: %v", err)
		}
		expiresAt = t
	}

	reason := req.Reason
	if reason == "" {
		reason = "operator_revoke"
	}

	// We can't easily detect "already revoked" because
	// PgRevocationStore.Revoke is ON CONFLICT DO NOTHING —
	// a duplicate call returns nil. For the response, we
	// optimistically claim newly_revoked=true; an audit
	// query confirms the canonical revoke timestamp if it
	// matters.
	if err := s.store.Revoke(ctx, req.Jti, expiresAt, reason); err != nil {
		log.Printf("[tokens] revoke jti=%s failed: %v", req.Jti, err)
		return nil, status.Errorf(codes.Internal, "revoke failed: %v", err)
	}
	log.Printf("[tokens] revoked jti=%s reason=%q expires_at=%s", req.Jti, reason, expiresAt.Format(time.RFC3339))

	return &pb.RevokeTokenResponse{
		NewlyRevoked: true,
		Message:      "jti added to revocation list; token will be rejected on next use",
	}, nil
}

// RefreshToken exchanges a valid refresh token for a new
// (access, refresh) pair. Phase 1.6 part B.
//
// Single-use rotation with reuse detection (closes the race in #1864):
// the input refresh token's jti is claimed — atomically revoked — BEFORE
// anything is minted, not after. Minting first (the prior order) let two
// concurrent exchanges of the same refresh token both pass
// ValidateRefreshToken (neither had been revoked yet) and both walk away
// with a valid new pair, contradicting the single-use contract documented
// in README.md. Revoking is now the claim: RevokeClaim's INSERT ... ON
// CONFLICT is the atomic check, so only one concurrent caller can ever
// win it.
//
// The loser — whose claim fails because the jti is already revoked —
// has just witnessed the credential presented twice. That's the same
// signal a stolen-and-replayed refresh token produces, and the two
// cases are indistinguishable from here, so the response is the same
// either way: revoke the entire rotation family (every token, past or
// future, that descends from the same original login), forcing a full
// re-authentication rather than guessing which presenter was
// legitimate. See RevocationStore.RevokeFamily.
//
// Unauthenticated endpoint by design: the refresh token IS the
// credential. Skip the access-token middleware on the
// /v1/tokens/refresh path; the daemon's HTTP middleware will need a
// route allowlist for this in a follow-up.
func (s *TokensServer) RefreshToken(ctx context.Context, req *pb.RefreshTokenRequest) (*pb.RefreshTokenResponse, error) {
	if req.RefreshToken == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token is required")
	}
	if s.tokenManager == nil {
		return nil, status.Error(codes.Unavailable, "token manager not configured")
	}

	// ValidateRefreshTokenClaims deliberately skips the revocation-list
	// check that ValidateRefreshToken enforces. Rotation makes the
	// presented jti revoked on success, so using the revocation-checked
	// validator here would reject a replay of an already-rotated token
	// at this step — before the reuse-detection logic below ever saw
	// it. The RevokeClaim call further down IS this handler's own
	// revocation check, and its failure IS the reuse signal.
	claims, err := s.tokenManager.ValidateRefreshTokenClaims(req.RefreshToken)
	if err != nil {
		log.Printf("[tokens] refresh denied: invalid token")
		return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
	}

	// familyID falls back to the token's own jti for a pre-family token
	// (minted before this fix) — it's the sole member of its own family,
	// so reuse detection still applies to it going forward.
	familyID := claims.FamilyID
	if familyID == "" {
		familyID = claims.ID
	}

	if s.store != nil && claims.ID != "" {
		if revoked, rerr := s.store.IsFamilyRevoked(ctx, familyID); rerr != nil {
			log.Printf("[tokens] refresh family lookup failed for family=%s: %v (continuing; the per-jti claim below is the primary gate)", familyID, rerr)
		} else if revoked {
			log.Printf("[tokens] refresh denied: family=%s already revoked", familyID)
			return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
		}

		exp := time.Time{}
		if claims.ExpiresAt != nil {
			exp = claims.ExpiresAt.Time
		}
		claimed, cerr := s.store.RevokeClaim(ctx, claims.ID, exp, "refresh_rotation")
		if cerr != nil {
			log.Printf("[tokens] refresh rotation claim failed for jti=%s: %v", claims.ID, cerr)
			return nil, status.Errorf(codes.Internal, "rotate failed: %v", cerr)
		}
		if !claimed {
			if ferr := s.store.RevokeFamily(ctx, familyID, "refresh_reuse_detected"); ferr != nil {
				log.Printf("[tokens] refresh reuse: failed to revoke family=%s: %v", familyID, ferr)
			}
			log.Printf("[tokens] refresh DENIED — reuse of already-rotated jti=%s; family=%s revoked", claims.ID, familyID)
			return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
		}
	}

	// The claim above is what makes this exchange exclusive; minting
	// after it is safe because we already own the only valid rotation
	// of this jti.
	newAccess, err := s.tokenManager.GenerateAccessToken(
