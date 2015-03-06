package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestSignChallenge_CloudProofContract(t *testing.T) {
	for _, keyType := range []string{ssh.KeyAlgoED25519, ssh.KeyAlgoRSA} {
		t.Run(keyType, func(t *testing.T) {
			var privateKey any
			var err error
			if keyType == ssh.KeyAlgoRSA {
				privateKey, err = rsa.GenerateKey(rand.Reader, 2048)
			} else {
				_, privateKey, err = ed25519.GenerateKey(rand.Reader)
			}
			if err != nil {
				t.Fatal(err)
			}
			signer, err := ssh.NewSignerFromKey(privateKey)
			if err != nil {
				t.Fatal(err)
			}
			block, err := ssh.MarshalPrivateKey(privateKey, "proof-test")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
				t.Fatal(err)
			}
			nonce := []byte{0, 255, 42, 17, 128, 1, 9, 10}
			proof, err := SignChallenge(path, nonce)
			if err != nil {
				t.Fatal(err)
			}
