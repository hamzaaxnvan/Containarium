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
