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
	}
	if len(got.Scopes) != 2 || got.Scopes[0] != "containers:read" {
		t.Errorf("scopes = %v", got.Scopes)
	}
	if got.TokenType != "access" {
		t.Errorf("tt = %q", got.TokenType)
	}
	if got.JTI != "test-jti-123" {
		t.Errorf("jti = %q", got.JTI)
	}
	if len(got.Audience) != 1 || got.Audience[0] != "containarium-api" {
		t.Errorf("aud = %v", got.Audience)
	}
	if got.IssuedAt.Unix() != 1700000000 {
		t.Errorf("iat = %d", got.IssuedAt.Unix())
	}
}

func TestClaimsFromRaw_AudAsString(t *testing.T) {
	// Some issuers serialize `aud` as a single string
	// rather than an array. JWT spec allows both.
	raw := map[string]any{
		"username": "alice",
		"aud":      "containarium-api",
	}
	got := claimsFromRaw(raw)
	if len(got.Audience) != 1 || got.Audience[0] != "containarium-api" {
		t.Fatalf("aud-as-string: got %v", got.Audience)
	}
}

func TestClaimsFromRaw_MissingFieldsStayZero(t *testing.T) {
	// Tokens without optional claims (legacy, no jti, no
	// scopes) must still decode without panic.
	raw := map[string]any{"username": "alice"}
	got := claimsFromRaw(raw)
