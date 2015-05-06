package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/footprintai/containarium/internal/sshconfig"
	"github.com/footprintai/containarium/internal/sshkey"
	"github.com/footprintai/containarium/pkg/core/incus"
	"golang.org/x/crypto/ssh"
)

func TestSSHRotate_CommandRegistered(t *testing.T) {
	cmd, _, err := sshCmd.Find([]string{"rotate"})
	if err != nil || cmd.Name() != "rotate" {
		t.Fatalf("ssh rotate command missing: command=%q, error=%v", cmd.Name(), err)
	}
	if cmd.RunE == nil || cmd.Flags().Lookup("name") == nil || cmd.Flags().Lookup("server") == nil {
		t.Fatal("ssh rotate must have a handler and name/server flags")
	}
}

// Verify the shipped cloud contract independently of the CLI signer.
func verifyRotationProof(publicKey string, signature, nonce []byte) error {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKey))
	if err != nil {
		return err
	}
	var sig ssh.Signature
	if err := ssh.Unmarshal(signature, &sig); err != nil {
		return err
	}
	return pub.Verify(append([]byte("containarium-ssh-key-proof-v1\x00"), nonce...), &sig)
}

func TestSSHRotate_HTTPFlow(t *testing.T) {
	for _, tc := range []struct {
		name                                            string
		challengeStatus, registrationStatus             int
		listStatus                                      int
		challengeBody, registrationBody, listBody       string
		wantOverlap                                     string
		wantErr, snakeCaseChallenge, wantNoRegistration bool
	}{
		{name: "success with key-only registration response"},
		{name: "snake_case challenge response", snakeCaseChallenge: true},
		{name: "signature rejected", registrationStatus: 400, wantErr: true},
		{name: "challenge unimplemented", challengeStatus: 501, wantErr: true, wantNoRegistration: true},
		{name: "challenge absent", challengeStatus: 404, wantErr: true, wantNoRegistration: true},
		{name: "challenge missing ID", challengeBody: `{"nonce":"AP8qEYABCQo="}`, wantErr: true, wantNoRegistration: true},
		{name: "challenge missing nonce", challengeBody: `{"challengeId":"challenge-1"}`, wantErr: true, wantNoRegistration: true},
		{name: "challenge empty nonce", challengeBody: `{"challengeId":"challenge-1","nonce":""}`, wantErr: true, wantNoRegistration: true},
		{name: "challenge invalid base64", challengeBody: `{"challengeId":"challenge-1","nonce":"%%%"}`, wantErr: true, wantNoRegistration: true},
		{name: "challenge malformed", challengeBody: `{`, wantErr: true, wantNoRegistration: true},
		{name: "wrong fingerprint", registrationBody: `{"key":{"fingerprint":"SHA256:other"}}`, wantErr: true},
		{name: "missing fingerprint", registrationBody: `{"key":{"name":"laptop"}}`, wantErr: true},
		{name: "registration missing key", registrationBody: `{}`, wantErr: true},
		{name: "registration null key", registrationBody: `{"key":null}`, wantErr: true},
		{name: "registration malformed", registrationBody: `{`, wantErr: true},
