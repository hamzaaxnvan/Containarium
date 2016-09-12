//go:build !containarium_client

package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
)

func TestMintDriverToken(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "jwt.secret")
	// A secret of adequate length (the token manager rejects short secrets).
	secret := "this-is-a-sufficiently-long-jwt-signing-secret-0123456789"
	if err := os.WriteFile(secretPath, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("mints a token the host's own daemon would accept", func(t *testing.T) {
