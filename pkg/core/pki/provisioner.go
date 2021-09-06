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

