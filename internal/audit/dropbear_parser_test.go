package audit

import (
	"testing"
	"time"
)

// #1189: in-box SSH session audit is incus-only. K8s boxes run dropbear, and
// the existing sshd parser cannot match a single line dropbear emits — so a
// K8s box yields zero login records, which reads identically to a box nobody
// logged into. For a compliance reader that is the difference between "no
// access occurred" and "access is not recorded".
//
// Every format below is taken from the dropbear 2022.83 binary's own format
// strings, not from documentation:
//
//	Password auth succeeded for '%s' from %s
//	Pubkey auth succeeded for '%s' with %s key %s from %s
//	Auth succeeded with blank password for '%s' from %s

const parseYear = 2026

// THE reason this parser exists: the sshd pattern matches nothing dropbear
// writes. If this ever stops being true, the second parser is dead weight.
func TestSSHDPatternCannotMatchDropbearOutput(t *testing.T) {
	for _, line := range []string{
		`Mar 12 14:30:01 box dropbear[42]: Pubkey auth succeeded for 'alice' with ssh-ed25519 key SHA256:abc from 10.0.0.1:54321`,
		`Mar 12 14:30:01 box dropbear[42]: Password auth succeeded for 'alice' from 10.0.0.1:54321`,
	} {
		if _, _, _, _, ok := parseAuthLogLine(line, parseYear); ok {
			t.Errorf("the sshd parser matched a dropbear line — if it genuinely handles both, this "+
				"second parser is unnecessary:\n%s", line)
		}
	}
}

func TestParseDropbearLine_Pubkey(t *testing.T) {
	line := `Mar 12 14:30:01 Pubkey auth succeeded for 'cld-alice' with ssh-ed25519 key SHA256:abc123 from 10.0.0.7:54321`

	ts, user, source, method, ok := parseDropbearLine(line, parseYear, time.Time{})
	if !ok {
		t.Fatal("failed to parse a pubkey login — the most common case on this platform, since " +
			"boxes are key-only")
	}
	if user != "cld-alice" {
		t.Errorf("user = %q, want cld-alice (dropbear single-quotes it, unlike sshd)", user)
	}
	if source != "10.0.0.7" {
		t.Errorf("source = %q, want the host without the port — storing \"10.0.0.7:54321\" as an "+
			"address breaks every query that groups by source", source)
	}
	if method != "publickey" {
		t.Errorf("method = %q, want publickey to match the sshd path's vocabulary", method)
	}
	if ts.Year() != parseYear || ts.Month() != time.March || ts.Day() != 12 {
		t.Errorf("timestamp = %v, want 2026-03-12", ts)
	}
}

func TestParseDropbearLine_Password(t *testing.T) {
	line := `Mar 12 14:30:01 Password auth succeeded for 'cld-bob' from 10.0.0.8:1234`
	_, user, source, method, ok := parseDropbearLine(line, parseYear, time.Time{})
	if !ok {
		t.Fatal("failed to parse a password login")
	}
	if user != "cld-bob" || source != "10.0.0.8" || method != "password" {
		t.Errorf("got user=%q source=%q method=%q", user, source, method)
	}
}

// A blank-password login is the single event an auditor most wants to see.
// Dropping it because its wording differs from the other two would hide
// exactly the wrong thing.
func TestParseDropbearLine_BlankPasswordIsCaptured(t *testing.T) {
	line := `Mar 12 14:30:01 Auth succeeded with blank password for 'cld-carol' from 10.0.0.9:2222`
	_, user, source, method, ok := parseDropbearLine(line, parseYear, time.Time{})
	if !ok {
		t.Fatal("a blank-password login was not recorded — the one login an auditor most needs")
	}
	if user != "cld-carol" || source != "10.0.0.9" {
		t.Errorf("got user=%q source=%q", user, source)
	}
	if method != "blank-password" {
		t.Errorf("method = %q; it must be distinguishable from a real password login", method)
	}
}
