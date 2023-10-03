package container

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ValidateSSHPublicKey verifies that the given string is a well-formed SSH
// public key (in OpenSSH authorized_keys format). Rejects obvious placeholder
