package anonbox

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var tokenNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func sampleToken() ClaimToken {
	return ClaimToken{BoxName: "anon-1a2b3c4d-container", FPHash: "ff00ff00", ExpiresAt: tokenNow.Add(4 * time.Hour), TokenID: "0123456789abcdef0123456789abcdef"}
}

func TestClaimToken_RoundTrip(t *testing.T) {
	s, err := MintClaimToken("secret-A", sampleToken())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, "v1.anon-1a2b3c4d-container.ff00ff00.") || strings.Count(s, ".") != 5 {
		t.Errorf("token shape = %s", s)
	}
	got, err := VerifyClaimToken("secret-A", s, tokenNow)
	if err != nil {
		t.Fatal(err)
	}
	if got != sampleToken() {
