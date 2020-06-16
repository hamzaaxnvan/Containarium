package integrity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func TestCompute_Unsigned(t *testing.T) {
	bin := writeTemp(t, "daemon", []byte("daemon-bytes"))
	prog := writeTemp(t, "policy.o", []byte("ebpf-object-bytes"))

	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	m, err := Compute(Inputs{
		BinaryPath:    bin,
		Programs:      []ProgramObject{{Name: "policy.o", Path: prog}},
		ConfigState:   map[string]string{"enforce": "1", "base_domain": "example.com"},
		DaemonVersion: "v0.28.0",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	// No signer → unsigned but still a complete measurement.
	if m.Signed {
		t.Errorf("expected unsigned measurement, got Signed=true")
	}
	if m.Signature != "" {
		t.Errorf("expected empty signature, got %q", m.Signature)
	}

	// Binary digest matches a direct sha256 of the file bytes.
	wantBin := sha256.Sum256([]byte("daemon-bytes"))
	if m.BinaryDigest != hex.EncodeToString(wantBin[:]) {
		t.Errorf("binary digest mismatch: got %s", m.BinaryDigest)
	}
	if len(m.ProgramDigests) != 1 || m.ProgramDigests[0].Name != "policy.o" {
		t.Fatalf("expected 1 program digest named policy.o, got %+v", m.ProgramDigests)
	}
