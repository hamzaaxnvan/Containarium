package audit

import (
	"strings"
	"testing"
)

// Phase 4.4 — Redact / SanitizeDetail tests (audit C-MED-5).

func TestRedact_LeavesSafeStringsAlone(t *testing.T) {
	safe := []string{
		"",
		"duration=123ms",
		"action=create container=alice",
		"hello world",
	}
	for _, s := range safe {
		if got := Redact(s); got != s {
			t.Errorf("Redact(%q) changed safe string to %q", s, got)
		}
	}
}

func TestRedact_ScrubsJWT(t *testing.T) {
	// Sample JWT-shaped string with three base64 segments.
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhbGljZSIsImV4cCI6OTk5OX0.signature_here_longer"
	got := Redact("token=" + jwt)
	if strings.Contains(got, jwt) {
		t.Fatalf("JWT not redacted: %q", got)
	}
	if !strings.Contains(got, "REDACTED-JWT") {
		t.Fatalf("expected redaction marker: %q", got)
	}
}

func TestRedact_ScrubsJWTWithBearerPrefix(t *testing.T) {
	in := "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhbGljZSIsImV4cCI6OTk5OX0.signature_here"
	got := Redact(in)
	if strings.Contains(got, "eyJhbGciOiJIUzI1NiJ9") {
		t.Fatalf("JWT body leaked: %q", got)
	}
}

func TestRedact_ScrubsPasswordVariants(t *testing.T) {
