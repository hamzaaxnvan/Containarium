package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 4.7 — ResolvePostgresURL / ResolvePostgresPassword.

func clearPGEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		envPostgresURL, envPostgresURLFile,
		envPostgresPassword, envPostgresPasswordFile,
	} {
		t.Setenv(k, "")
	}
}

// --- URL resolution ---

func TestResolvePostgresURL_PrefersFileOverEnv(t *testing.T) {
	clearPGEnv(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "dsn")
	if err := os.WriteFile(p, []byte("postgres://from-file/db\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	t.Setenv(envPostgresURL, "postgres://from-env/db")
	t.Setenv(envPostgresURLFile, p)

	dsn, source, err := ResolvePostgresURL()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if dsn != "postgres://from-file/db" {
		t.Fatalf("dsn = %q; want file value", dsn)
	}
	if !strings.HasPrefix(source, "file:") {
		t.Fatalf("source = %q; want file:", source)
	}
}

func TestResolvePostgresURL_FallsBackToEnv(t *testing.T) {
	clearPGEnv(t)
	t.Setenv(envPostgresURL, "postgres://from-env/db")
	dsn, source, err := ResolvePostgresURL()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if dsn != "postgres://from-env/db" || source != "env" {
		t.Fatalf("dsn=%q source=%q", dsn, source)
	}
}

func TestResolvePostgresURL_EmptyMeansAutoDetect(t *testing.T) {
	clearPGEnv(t)
	dsn, source, err := ResolvePostgresURL()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if dsn != "" || source != "auto-detect" {
		t.Fatalf("dsn=%q source=%q; want empty + auto-detect", dsn, source)
	}
}

func TestResolvePostgresURL_RejectsInsecureFilePerms(t *testing.T) {
	clearPGEnv(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "dsn")
	if err := os.WriteFile(p, []byte("postgres://x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	t.Setenv(envPostgresURLFile, p)
	_, _, err := ResolvePostgresURL()
	if err == nil {
		t.Fatal("0644 file should be rejected")
	}
	if !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("error should mention insecure permissions; got %v", err)
