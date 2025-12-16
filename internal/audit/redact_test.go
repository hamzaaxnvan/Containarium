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
