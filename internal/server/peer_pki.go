package server

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/sentinel"
)

// Phase 0.5 — peer-to-peer mTLS bootstrap on the daemon side.
//
// At PeerPool startup the daemon:
//  1. POSTs /sentinel/peer-cert with its own peer ID (HMAC-signed
//     via auth.SignSentinelRequest) and receives a freshly-minted
//     leaf cert + key plus the CA bundle.
//  2. Persists them to a known on-disk path (mode 0600 / 0700) so
//     a daemon restart doesn't burn a fresh issuance.
//  3. Builds an *http.Client pinned to the CA and presenting the
//     leaf cert, ready for peer-to-peer HTTPS calls.
//
// The peer client in peer.go uses the daemon's HTTPS client when
// `CONTAINARIUM_SENTINEL_URL` starts with `https://`. Plain HTTP
// remains the default for backwards compatibility during rollout.
// See docs/security/ZERO-TRUST-AUDIT.md C-CRIT-1.

// peerPKI holds the daemon-side cert material — refreshable.
type peerPKI struct {
	mu      sync.RWMutex
	tlsCert tls.Certificate
	caPool  *x509.CertPool
	caPEM   []byte
	expiry  time.Time
}

// CACertPool returns the trusted CA pool for verifying sentinel +
// peer server certs. Returns nil if no PKI is configured.
func (p *peerPKI) CACertPool() *x509.CertPool {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.caPool
}

// ClientCertificate returns the daemon's leaf cert + key for mTLS.
// Returns the zero-value tls.Certificate if no PKI is configured —
// callers can use that to decide whether to present a client cert.
func (p *peerPKI) ClientCertificate() tls.Certificate {
	if p == nil {
		return tls.Certificate{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.tlsCert
}

// IdentitySigner returns the daemon's leaf private key as a
// crypto.Signer (the sentinel-issued ECDSA peer leaf) plus the PEM
// of the leaf cert, for callers that need to sign with the node's
// identity key — e.g. the integrity self-measurement (#683). Returns
// (nil, "") when no PKI is configured or the leaf key doesn't
// implement crypto.Signer.
func (p *peerPKI) IdentitySigner() (crypto.Signer, string) {
	if p == nil {
		return nil, ""
	}
	p.mu.RLock()
	cert := p.tlsCert
	p.mu.RUnlock()

	signer, ok := cert.PrivateKey.(crypto.Signer)
	if !ok || len(cert.Certificate) == 0 {
		return nil, ""
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	return signer, string(leafPEM)
}

// CACertPEM returns the PEM bytes of the trusted CA, for callers
// that need to log the pin or persist it.
func (p *peerPKI) CACertPEM() []byte {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.caPEM
}

// Expiry reports when the current leaf cert expires. Used by the
// renewal loop.
func (p *peerPKI) Expiry() time.Time {
	if p == nil {
		return time.Time{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.expiry
}

// replace swaps in fresh cert material atomically. The caller has
// already validated the cert; this just commits.
func (p *peerPKI) replace(cert tls.Certificate, caPool *x509.CertPool, caPEM []byte, expiry time.Time) {
	p.mu.Lock()
	p.tlsCert = cert
	p.caPool = caPool
	p.caPEM = caPEM
	p.expiry = expiry
	p.mu.Unlock()
}

// FetchPeerPKI asks the sentinel to mint a leaf cert for `peerID`
// and returns a populated peerPKI ready for use. Authenticates the
// request with the sentinel HMAC secret. `sentinelBaseURL` is the
// sentinel's HTTP[S] base URL (e.g. https://sentinel:8889).
//
// The request is signed even if the URL is plain http://, so
// rollout deployments can use HTTP for the bootstrap fetch and
// HTTPS for steady-state peer traffic — the bootstrap is HMAC-
// authenticated regardless. (We still recommend HTTPS for the
// bootstrap call too, since the leaf private key travels in the
// body.)
func FetchPeerPKI(ctx context.Context, sentinelBaseURL, peerID string, hmacSecret []byte) (*peerPKI, error) {
	if len(hmacSecret) < auth.SentinelMinSecretLen {
		return nil, fmt.Errorf("sentinel HMAC secret is missing or too short — Phase 0.5 bootstrap requires CONTAINARIUM_SENTINEL_AUTH_SECRET")
	}
	if peerID == "" {
		return nil, fmt.Errorf("peerID is required")
	}

	body, err := json.Marshal(sentinel.PeerCertRequest{PeerID: peerID})
	if err != nil {
		return nil, fmt.Errorf("marshal cert request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sentinelBaseURL+"/sentinel/peer-cert", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build peer-cert request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	auth.SignSentinelRequest(req, hmacSecret)
