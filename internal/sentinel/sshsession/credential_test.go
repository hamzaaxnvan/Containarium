package sshsession

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"testing"

	"golang.org/x/crypto/ssh"
)

// newTestCA returns an ssh.Signer usable as a certificate authority.
func newTestCA(t *testing.T) ssh.Signer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("build CA signer: %v", err)
	}
	return signer
}

// newTestUserCert signs a fresh ed25519 key as a user certificate with the
// given key_id/serial, using ca as the signing authority.
func newTestUserCert(t *testing.T, ca ssh.Signer, keyID string, serial uint64) (*ssh.Certificate, ssh.PublicKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate user key: %v", err)
	}
	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("build user public key: %v", err)
