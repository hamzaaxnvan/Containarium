package sshkey

import (
	"strings"

	"golang.org/x/crypto/ssh"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ParseType classifies an OpenSSH public key (an authorized_keys line,
// options prefix allowed) as the SSH authorization contract's key type.
// The key must parse: a line that only looks like a known type, an
// unsupported algorithm (ssh-dss, certificates), or garbage is
// SSH_KEY_TYPE_UNSPECIFIED. The two FIDO2 types are the OpenSSH
// vendor-suffixed names sk-ssh-ed25519@openssh.com and
// sk-ecdsa-sha2-nistp256@openssh.com (#2517).
func ParseType(pub string) pb.SSHKeyType {
