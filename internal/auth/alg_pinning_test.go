package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Regression coverage for Phase 0.3 (finding A-CRIT-3): the JWT
// validator must reject any algorithm other than HS256.
//
// The pre-hardening code accepted any HMAC variant — a future
// library or dependency bug could have escalated that to a full
// signature-bypass (alg=none, alg=RS256-with-public-key-as-HMAC-key,
// etc.). With the pin in place those vectors are closed at the
// validator, regardless of what the underlying library tolerates.

func mintToken(t *testing.T, method jwt.SigningMethod, secret interface{}) string {
	t.Helper()
	claims := Claims{
		Username: "alice",
		Roles:    []string{"user"},
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "containarium-test",
			Audience:  jwt.ClaimStrings{DefaultAudience},
		},
	}
	tok := jwt.NewWithClaims(method, claims)
	s, err := tok.SignedString(secret)
	if err != nil {
		t.Fatalf("sign with %s: %v", method.Alg(), err)
	}
	return s
}

func TestValidateToken_AcceptsHS256(t *testing.T) {
	tm, _ := NewTokenManager("a-secret-that-is-at-least-32-bytes-long!!", "containarium-test")
