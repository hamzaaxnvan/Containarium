package sshkey

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// GenerateFresh creates a distinct keypair without replacing previous keys.
func GenerateFresh(opts LocateOpts) (pubPath, pub string, err error) {
	return generate(opts, false, containariumKeyName+"_"+rand.Text())
}

// RemoveRetired removes only CLI-owned keypairs explicitly retired by the
// server. Personal keys and unknown or merely expired keys are retained.
func RemoveRetired(opts LocateOpts, keys []SSHKey, now time.Time) ([]string, error) {
	retired, active := make(map[string]bool), make(map[string]bool)
	for _, key := range keys {
		if key.Fingerprint == "" {
			continue
		}
		if key.RetiredAt != nil && !key.RetiredAt.IsZero() && !key.RetiredAt.After(now) {
