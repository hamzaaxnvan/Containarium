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
