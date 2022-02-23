package server

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Phase 2.5 follow-up — OTel bearer load/create primitive.

func resetBearerCache(t *testing.T) {
	t.Helper()
	otelBearerOnce = sync.Once{}
	otelBearerValue = ""
	otelBearerError = nil
}

func clearOTelEnv(t *testing.T) {
	t.Helper()
	t.Setenv(otelBearerEnvOverride, "")
	t.Setenv(otelBearerEnvFile, "")
}

func TestLoadOrCreateOTelBearer_EnvOverride(t *testing.T) {
	resetBearerCache(t)
	clearOTelEnv(t)
	t.Setenv(otelBearerEnvOverride, "from-env")
	v, err := LoadOrCreateOTelBearer()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if v != "from-env" {
		t.Fatalf("v = %q; want from-env", v)
	}
}

func TestLoadOrCreateOTelBearer_FileSource(t *testing.T) {
	resetBearerCache(t)
	clearOTelEnv(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "bearer")
	if err := os.WriteFile(p, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(otelBearerEnvFile, p)

	v, err := LoadOrCreateOTelBearer()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if v != "from-file" {
		t.Fatalf("v = %q; want from-file", v)
	}
}

func TestLoadOrCreateOTelBearer_RejectsInsecureFile(t *testing.T) {
	resetBearerCache(t)
	clearOTelEnv(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "bearer")
	if err := os.WriteFile(p, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(otelBearerEnvFile, p)

	_, err := LoadOrCreateOTelBearer()
	if err == nil {
		t.Fatal("0644 file should be rejected")
	}
	if !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("error should mention insecure permissions; got %v", err)
	}
}

func TestLoadOrCreateOTelBearer_EmptyFileRejected(t *testing.T) {
	resetBearerCache(t)
	clearOTelEnv(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "bearer")
	if err := os.WriteFile(p, []byte(""), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(otelBearerEnvFile, p)

	_, err := LoadOrCreateOTelBearer()
	if err == nil {
