// Package sshkey detects, generates, and fingerprints local SSH
// keypairs for the `containarium ssh setup` flow (sub-task A5 of
// umbrella-issue #100). See prd/cloud/cli-login-and-multi-env-ssh.md
// §"Design — Multi-environment SSH".
//
// Why a dedicated package: the CLI flow needs three slightly-different
// things from "the user's local SSH key" — locate an existing one,
// generate a new one on first login, and compute the SHA256 fingerprint
// so the cloud-side ListSSHKeys table is human-meaningful. Concentrating
// all three in one place gives `internal/cmd/ssh.go` and the
// `--with-ssh-setup` path in login.go a single, testable surface.
//
// Strong typing per CLAUDE.md: the public shape is the SSHKey struct
// (which mirrors the cloud's UserService SSHKey message field-for-field),
// not a (string, string, string) triple.
package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHKey is the wire-shape of a user-registered SSH key as returned by
// the cloud's UserService.ListSSHKeys / AddSSHKey. Kept here (rather
// than in pkg/pb) so the CLI compiles cleanly even before the
// cloud-side proto/pb regen lands; the field tags match the JSON the
// REST shim will emit.
type SSHKey struct {
	Name        string    `json:"name"`
	PublicKey   string    `json:"publicKey"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"createdAt,omitempty"`
	ExpiresAt   time.Time `json:"expiresAt,omitempty"`
	// Expiry alone does not authorize local private-key deletion.
	RetiredAt *time.Time `json:"retiredAt,omitempty"`
}

// UnmarshalJSON accepts the gateway's camelCase fields and the proto field
// names used by servers configured to emit snake_case JSON.
func (key *SSHKey) UnmarshalJSON(data []byte) error {
	type keyJSON SSHKey
	var wire struct {
		keyJSON
		PublicKey string     `json:"public_key"`
		CreatedAt time.Time  `json:"created_at"`
		ExpiresAt time.Time  `json:"expires_at"`
		RetiredAt *time.Time `json:"retired_at"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*key = SSHKey(wire.keyJSON)
	if key.PublicKey == "" {
		key.PublicKey = wire.PublicKey
	}
