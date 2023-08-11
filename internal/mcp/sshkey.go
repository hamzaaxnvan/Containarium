package mcp

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// generateEphemeralSSHKey creates an ed25519 keypair client-side and
// returns the OpenSSH-formatted public key (authorized_keys line) plus
// the OpenSSH PEM-encoded private key.
//
