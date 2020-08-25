package mcp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// genTestCAPEM produces a tiny self-signed CA cert in PEM form
// for the CA-pin test below. Throwaway — never used to actually
// verify anything beyond AppendCertsFromPEM parseability.
func genTestCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mcp-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// Phase 2.2 — MCP client refuses plaintext HTTP by default and
// supports CA pinning via env var. Audit C-HIGH-1.

func TestBuildMCPTLSConfig_RefusesHTTPByDefault(t *testing.T) {
	// Override the package-init helper that allows insecure for
	// the rest of the test suite — we want to exercise the strict
	// path here.
	t.Setenv(mcpAllowInsecureEnv, "")
	cfg, err := buildMCPTLSConfig("http://daemon.example.com:8080")
	if err == nil {
		t.Fatal("http:// baseURL must be refused by default")
	}
	if cfg != nil {
		t.Fatal("config should be nil when the URL is refused")
	}
	if !strings.Contains(err.Error(), mcpAllowInsecureEnv) {
		t.Fatalf("error should name the escape-hatch env var: %v", err)
	}
}

func TestBuildMCPTLSConfig_AllowsHTTPWhenOptedIn(t *testing.T) {
	t.Setenv(mcpAllowInsecureEnv, "true")
	cfg, err := buildMCPTLSConfig("http://daemon.example.com:8080")
	if err != nil {
