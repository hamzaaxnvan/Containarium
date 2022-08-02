package sshsession

import (
	"fmt"

	"golang.org/x/crypto/ssh"
)

// ExtractCredential parses the raw wire-format public key bytes sshpiperd
