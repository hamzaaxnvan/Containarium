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
	wire := ssh.Marshal(body)
	return keyType + " " + base64.StdEncoding.EncodeToString(wire) + " user@security-key"
}

func TestParseType(t *testing.T) {
	edPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey := func(c elliptic.Curve) *ecdsa.PublicKey {
		k, err := ecdsa.GenerateKey(c, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return &k.PublicKey
	}
	p256 := ecKey(elliptic.P256())

	skEd := skLine(t, ssh.KeyAlgoSKED25519, struct {
		Name        string
		KeyBytes    []byte
		Application string
	}{ssh.KeyAlgoSKED25519, edPub, "ssh:"})
	skEc := skLine(t, ssh.KeyAlgoSKECDSA256, struct {
		Name        string
		ID          string
