package connectcore

import (
	"errors"
	"strings"
	"testing"
)

func TestNewMarker_UniqueAndHex(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		m, err := NewMarker()
		if err != nil {
			t.Fatalf("NewMarker: %v", err)
		}
		if len(m) != 16 {
			t.Fatalf("marker %q len = %d, want 16 hex chars", m, len(m))
		}
		for _, r := range m {
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				t.Fatalf("marker %q has non-hex char %q", m, r)
			}
		}
		if seen[m] {
			t.Fatalf("duplicate marker %q", m)
		}
		seen[m] = true
	}
}

func TestEncodeCommand_RoundTripSafe(t *testing.T) {
	// The encoding must be free of characters that need shell quoting, so it
	// survives ssh + remote shell + tmux unquoted.
	for _, cmd := range []string{`echo "hi"`, `cd /a/b && ls -la | grep x`, "a'b\"c$d`e", "weird\nnewline"} {
		enc := EncodeCommand(cmd)
		for _, r := range enc {
			switch {
			case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '+', r == '/', r == '=':
				continue
			default:
				t.Fatalf("EncodeCommand(%q) produced shell-unsafe char %q in %q", cmd, r, enc)
			}
		}
	}
}

func TestValidateSessionName(t *testing.T) {
	ok := []string{"work", "build-1", "a_b", "S"}
	for _, n := range ok {
		if err := ValidateSessionName(n); err != nil {
			t.Errorf("ValidateSessionName(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{"", "has space", "dot.name", "colon:name", strings.Repeat("x", 65)}
	for _, n := range bad {
		if err := ValidateSessionName(n); err == nil {
			t.Errorf("ValidateSessionName(%q) = nil, want error", n)
		}
	}
}

func TestBuildSessionExecArgs(t *testing.T) {
	target := Target{User: "cld-x", Host: "h.example.com", Port: 22}
	args := BuildSessionExecArgs(target, "/k/id", "work", "deadbeef", "Y21k", 60)
	joined := strings.Join(args, " ")
	for _, want := range []string{"cld-x@h.example.com", "bash -s -- work deadbeef Y21k 60", "-i /k/id", "accept-new"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
	// The remote command tail must come after the destination.
	if !strings.Contains(joined, "cld-x@h.example.com bash -s --") {
		t.Errorf("bash -s should follow the destination: %q", joined)
	}
}
