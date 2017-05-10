package container

import (
	"strings"
	"testing"
)

// Phase 3.5 — explicit CR/LF rejection in ValidateSSHPublicKey
// (audit B-MED-3). The pre-3.5 behavior happened to reject these
// keys because ssh.ParseAuthorizedKey's base64 decoder couldn't
// stomach embedded newlines, but the rejection was incidental.
// Make it explicit so a future parser change can't silently
// re-open the injection vector.

func TestValidateSSHPublicKey_RejectsEmbeddedLF(t *testing.T) {
	// Synthesize a key with a sneaky LF in the middle. The exact
	// bytes don't matter — the validator should reject before
	// it ever tries to parse.
	key := "ssh-ed25519 AAAA\ncommand=\"rm -rf /\" ssh-ed25519 BBBB user@host"
	err := ValidateSSHPublicKey(key)
	if err == nil {
		t.Fatal("embedded LF must be rejected")
	}
	if !strings.Contains(err.Error(), "newline") {
		t.Fatalf("error should name newline: %v", err)
	}
}

func TestValidateSSHPublicKey_RejectsEmbeddedCR(t *testing.T) {
	key := "ssh-ed25519 AAAA\rsomething ssh-ed25519 BBBB user@host"
	err := ValidateSSHPublicKey(key)
	if err == nil {
		t.Fatal("embedded CR must be rejected")
