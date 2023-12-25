package pki

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"
	"time"
)

func newTestProvisioner(t *testing.T, expiry time.Duration) *Provisioner {
	t.Helper()
	keyPEM, err := GenerateCAKey()
	if err != nil {
		t.Fatalf("GenerateCAKey: %v", err)
	}
	p, err := NewFromKey(keyPEM, expiry)
	if err != nil {
		t.Fatalf("NewFromKey: %v", err)
	}
	return p
}

func TestIssuedPeerCert_VerifiesAgainstCA(t *testing.T) {
	p := newTestProvisioner(t, 24*time.Hour)
	certPEM, keyPEM, err := p.IssuePeerCert("tunnel-node-a-gpu", nil, nil)
	if err != nil {
		t.Fatalf("IssuePeerCert: %v", err)
	}

	// Build a fresh CA pool with just our CA, then verify the leaf.
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(p.CACertPEM()) {
