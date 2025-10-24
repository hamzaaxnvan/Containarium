package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Phase 5.4 — abuse-case regression tests. Each scenario
// names a class of attack the audit explicitly flagged and
// asserts that the daemon's defense holds. If any of these
// flip from "deny" to "allow" in a future refactor, the
// build breaks loudly. That's the point.
//
// These tests aren't trying to cover the full code path —
// per-layer tests already do that. They're a tripwire: a
// single regression here means an attack we already
// audited is suddenly viable again.

func abuseTokenManager(t *testing.T) *TokenManager {
	t.Helper()
	tm, err := NewTokenManager("abuse-suite-secret-key-at-least-32-bytes-long", "abuse-test")
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	return tm
}

// --- Replayed revoked tokens (Phase 1.2) ---

func TestAbuse_RevokedTokenIsRejected(t *testing.T) {
	tm := abuseTokenManager(t)
	store := newAbuseStore()
	tm.SetRevocationStore(store)

	tok, err := tm.GenerateAccessToken("alice", []string{"user"}, time.Hour)
	if err != nil {
		t.Fatalf("GenerateAccessToken: %v", err)
	}
	claims, err := tm.ValidateAccessToken(tok)
	if err != nil {
		t.Fatalf("token should validate before revoke; got %v", err)
	}
	if err := tm.RevokeToken(context.Background(), claims, "abuse-test"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if _, err := tm.ValidateAccessToken(tok); err == nil {
		t.Fatal("revoked token must NOT validate on the API surface")
	}
}

// --- Refresh-token replay (Phase 1.6 rotation) ---

func TestAbuse_RefreshTokenSingleUse(t *testing.T) {
	// We can't drive the gRPC RefreshToken handler from this
	// package without cycles, but the underlying invariant
	// is simple: once a refresh token's jti is revoked,
	// ValidateRefreshToken refuses it. That's what the RPC
	// does on rotation. Asserting it here keeps the
	// invariant under test even if the RPC code is
	// restructured.
	tm := abuseTokenManager(t)
	store := newAbuseStore()
	tm.SetRevocationStore(store)

	refresh, _ := tm.GenerateRefreshToken("alice", []string{"user"}, time.Hour)
	claims, err := tm.ValidateRefreshToken(refresh)
	if err != nil {
		t.Fatalf("first validate: %v", err)
	}

	// Simulate rotation: the RPC revokes the prior refresh.
	if err := tm.RevokeToken(context.Background(), claims, "refresh_rotation"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	// Replay the same refresh — must fail.
	if _, err := tm.ValidateRefreshToken(refresh); err == nil {
		t.Fatal("replayed refresh token must be rejected after rotation")
	}
}

// --- Access token used at refresh endpoint (Phase 1.6) ---

func TestAbuse_AccessTokenAtRefreshPathRejected(t *testing.T) {
	tm := abuseTokenManager(t)
	tok, _ := tm.GenerateAccessToken("alice", []string{"user"}, time.Hour)
	if _, err := tm.ValidateRefreshToken(tok); err == nil {
		t.Fatal("access token must NOT pass ValidateRefreshToken")
	}
}

// --- Refresh token used on API surface (Phase 1.6) ---

func TestAbuse_RefreshTokenOnAPISurfaceRejected(t *testing.T) {
	tm := abuseTokenManager(t)
