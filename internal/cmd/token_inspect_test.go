package cmd

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// Unit tests for the inspect decoder. The decode path is
// the security-sensitive part — an operator pasting a
// leaked token must get the right jti out, even if the
// token has been hand-edited or uses unusual claim shapes.

func TestClaimsFromRaw_FullClaimSet(t *testing.T) {
	raw := map[string]any{
		"username": "alice",
		"sub":      "alice",
		"roles":    []any{"admin", "user"},
		"scopes":   []any{"containers:read", "secrets:read"},
		"tt":       "access",
		"iss":      "containarium",
		"aud":      []any{"containarium-api"},
		"jti":      "test-jti-123",
		"iat":      float64(1700000000),
		"nbf":      float64(1700000000),
		"exp":      float64(1700003600),
	}
	got := claimsFromRaw(raw)

	if got.Username != "alice" {
		t.Errorf("username = %q", got.Username)
	}
	if len(got.Roles) != 2 || got.Roles[0] != "admin" {
		t.Errorf("roles = %v", got.Roles)
