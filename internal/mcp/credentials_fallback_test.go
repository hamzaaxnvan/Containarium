package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/footprintai/containarium/internal/credentials"
)

// writeCredsFile is a helper that drops a credentials.json under
// $HOME (which the test t.Setenvs to a temp dir, so DefaultPath()
// resolves there) with the given default server + entries.
func writeCredsFile(t *testing.T, home string, defaultServer string, servers map[string]credentials.ServerCreds) {
	t.Helper()
	t.Setenv("HOME", home)
	cf := credentials.NewCredentialsFile()
	for srv, creds := range servers {
		cf.Set(srv, creds)
	}
	if defaultServer != "" {
		cf.DefaultServer = credentials.NormalizeServer(defaultServer)
	}
	if err := os.MkdirAll(filepath.Join(home, ".containarium"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := credentials.Save(filepath.Join(home, credentials.DefaultRelPath), cf); err != nil {
		t.Fatalf("save: %v", err)
	}
}

func TestLoadConfig_CredentialsFallback_PopulatesToken(t *testing.T) {
	t.Setenv("CONTAINARIUM_JWT_TOKEN", "")
	t.Setenv("CONTAINARIUM_JWT_TOKEN_FILE", "")
	t.Setenv("CONTAINARIUM_SERVER_URL", "https://cloud.example.com")
	home := t.TempDir()
	writeCredsFile(t, home, "https://cloud.example.com", map[string]credentials.ServerCreds{
		"https://cloud.example.com": {Token: "ctnr_abc.secret"},
	})

	c := LoadConfig()
	if c.JWTToken != "ctnr_abc.secret" {
