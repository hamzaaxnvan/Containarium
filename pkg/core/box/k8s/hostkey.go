package k8s

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// The box runs dropbear with BOTH an ed25519 and an RSA host key: dropbear
// crashes (rsa.c NULL assert) if a client offers rsa-sha2 and it has no RSA
// key, and sshpiper's Go client does offer rsa-sha2 and negotiates it. So both
// keys must be stable and pinned, or the gateway's known_hosts check fails on
// whichever key sshpiper picks. Verified live.

// generateEd25519HostKey makes an ed25519 host key: OpenSSH-PEM private (the
// box entrypoint feeds it to dropbearconvert) + authorized-key public.
func generateEd25519HostKey() (privPEM []byte, pubAuthorized string, err error) {
