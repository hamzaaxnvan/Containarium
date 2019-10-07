package container

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ValidateSSHPublicKey verifies that the given string is a well-formed SSH
// public key (in OpenSSH authorized_keys format). Rejects obvious placeholder
// strings, keys with embedded newlines (an injection vector into the
// authorized_keys file), and keys with malformed base64 payloads.
//
// Audit B-MED-3: ssh.ParseAuthorizedKey happens to reject embedded
// `\n` / `\r` today because the base64 layer doesn't tolerate them,
// but that's incidental — a future loosening (or a parser that
// runs `strings.TrimSpace` upstream and feeds us multi-line input)
// would re-open the door. The explicit check makes the intent
// load-bearing rather than implicit.
func ValidateSSHPublicKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
