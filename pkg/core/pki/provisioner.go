// Package pki implements the Containarium peer-CA used to issue
// short-lived certificates for sentinel↔daemon and peer-to-peer
// HTTPS. Design summary: a single operator-managed RSA private
// key on the sentinel acts as the CA, the CA certificate is
// generated at runtime from that key, and per-peer client/server
// certs are minted on demand with a configurable short TTL
// (default 7 days). No CRL or OCSP — rotation happens before any
// leaf could be meaningfully abused.
//
// The pattern intentionally avoids:
//   - Bundling a separate ca.crt file (only the key needs storing).
//   - An enrollment ceremony with PKCS#10 CSRs (overkill for a
//     two-tier control-plane / data-plane topology where peer
//     identity is already established at the tunnel layer).
//   - Long-lived end-entity certs (a fleet that can lose a daemon
//     for 7 days has bigger problems than cert rotation).
//
// Cert-building primitives are reused from
// github.com/footprintai/go-certs/pkg/certs/gen — the same
// library already used for the daemon's gRPC mTLS path
// (internal/mtls/loader.go). What we add on top is the "CA cert
// is regenerated at runtime from just the key" ergonomics, which
// keeps operators to one secret file instead of two.
//
// See docs/security/ZERO-TRUST-AUDIT.md C-CRIT-1 for the threat
// model this package closes.
package pki

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net"
	"time"

	certsgen "github.com/footprintai/go-certs/pkg/certs/gen"
)

// DefaultLeafExpiry is the default TTL for peer / server
// end-entity certs. Short enough that a leaked key has limited
// blast radius; long enough that an off-by-a-day clock won't
// brick the fleet. Renewal kicks in at 2/3 of this.
const DefaultLeafExpiry = 7 * 24 * time.Hour

// CAValidity is the lifetime of the self-signed CA cert generated
// at runtime from the operator's RSA key. 10 years is long enough
// that the CA cert itself never expires in normal operation; the
// only durable secret is the RSA key on disk, which the operator
// rotates by replacing the file and restarting the sentinel.
const CAValidity = 10 * 365 * 24 * time.Hour

// orgName goes into the Subject of every cert this CA issues —
// looks intelligible in `openssl x509 -text` output during
// incident response.
const orgName = "Containarium CA"

// Provisioner issues per-peer X.509 certificates signed by the
// sentinel's CA. Only the sentinel holds the CA private key;
// daemons receive only the CA certificate (for verification) and
// their own leaf cert + key (for mTLS).
type Provisioner struct {
	caCert    *x509.Certificate
	caKey     *rsa.PrivateKey
	caCertPEM []byte
	expiry    time.Duration
}

// NewFromKey builds a Provisioner from just the CA private key.
// The CA certificate is generated at runtime — operators only need
// to manage the key file (mode 0400, single backup, off-host copy
// for disaster recovery).
//
// `caKeyPEM` must be either an "RSA PRIVATE KEY" (PKCS#1) or a
// "PRIVATE KEY" (PKCS#8) block. `expiry` is the leaf TTL; pass 0
// to use DefaultLeafExpiry.
func NewFromKey(caKeyPEM []byte, expiry time.Duration) (*Provisioner, error) {
	caKey, err := parseRSAKey(caKeyPEM)
	if err != nil {
		return nil, err
	}
	if expiry <= 0 {
		expiry = DefaultLeafExpiry
	}

	// Use certsgen.CertTemplate for the serial-number + base
	// fields, then mark this template as a self-signed CA. (The
	// library doesn't expose a "make me a CA template" helper, so
	// we set the CA-specific bits ourselves — short and explicit.)
	now := time.Now()
	caTemplate, err := certsgen.CertTemplate(
		now.Add(-1*time.Minute), // backdate to absorb sentinel↔daemon clock skew
		now.Add(CAValidity),
		certsgen.WithOrganizations(orgName),
	)
	if err != nil {
		return nil, fmt.Errorf("CA cert template: %w", err)
	}
	caTemplate.Subject.CommonName = "Containarium Peer CA"
	caTemplate.IsCA = true
	caTemplate.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	caTemplate.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}

	caCert, caCertPEM, err := certsgen.CreateCert(caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("create CA cert: %w", err)
	}

	return &Provisioner{
		caCert:    caCert,
		caKey:     caKey,
		caCertPEM: caCertPEM,
		expiry:    expiry,
	}, nil
}

// IssuePeerCert mints a leaf certificate for the named peer.
