package sshkey

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"testing"

	"golang.org/x/crypto/ssh"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// authorizedLine renders an ssh.PublicKey as an authorized_keys line.
func authorizedLine(t *testing.T, pub interface{}) string {
	t.Helper()
	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("NewPublicKey: %v", err)
	}
	return string(ssh.MarshalAuthorizedKey(pk))
}

// skLine builds a FIDO2 security-key public key line by hand: x/crypto
// parses both sk- types but has no constructor for them.
func skLine(t *testing.T, keyType string, body interface{}) string {
	t.Helper()
