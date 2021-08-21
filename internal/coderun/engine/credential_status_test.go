package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCredentialStatusScript_UnknownEngine pins that an engine with no probe
// fails closed with ok=false, never a guessed answer. Codex had no probe
// until #2277 (it is covered by its own TestCredentialStatusScript_Codex
// below now), so this uses a name no engine will ever have.
func TestCredentialStatusScript_UnknownEngine(t *testing.T) {
	if _, ok := CredentialStatusScript(Name("not-a-real-engine")); ok {
		t.Error("an engine with no probe should report ok=false, not a script")
	}
}

// TestParseCredentialStatusSource pins the three valid values and rejects
// anything else loudly — "defaulting" an unrecognized probe output to "none"
// would silently misreport a box that actually has a credential.
func TestParseCredentialStatusSource(t *testing.T) {
	for _, v := range []CredentialStatusSource{CredentialStatusInteractive, CredentialStatusAPIKey, CredentialStatusNone} {
		got, err := ParseCredentialStatusSource(string(v))
		if err != nil {
			t.Errorf("ParseCredentialStatusSource(%q) error: %v", v, err)
		}
		if got != v {
			t.Errorf("ParseCredentialStatusSource(%q) = %q", v, got)
		}
	}
	if _, err := ParseCredentialStatusSource("some-token-value"); err == nil {
		t.Error("an unrecognized probe output must be an error, not silently accepted")
	}
	if _, err := ParseCredentialStatusSource(""); err == nil {
		t.Error("empty output must be an error")
	}
}

// TestCredentialStatusScript_Claude is an end-to-end proof against a REAL
// shell (not a string match on the script) — the same style
// session_discovery_test.go uses for the same reason: the interesting bug
// here is a shell-quoting or logic mistake that a string comparison of the
// rendered script would never catch.
//
// Each case asserts the script prints EXACTLY one line and never more — the
// contract this probe exists to guarantee (a credential's value must never
// appear in its output).
func TestCredentialStatusScript_Claude(t *testing.T) {
	script, ok := CredentialStatusScript(NameClaude)
	if !ok {
		t.Fatal("CredentialStatusScript(NameClaude) ok=false")
	}

	tests := []struct {
		name string
		seed func(home string)
		want CredentialStatusSource
	}{
		{
			name: "nothing present",
			seed: func(string) {},
			want: CredentialStatusNone,
		},
		{
			name: "interactive sign-in present",
