package sentinel

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/gateway"
)

// CertStore holds TLS certificates synced from the backends (Caddy/Let's
// Encrypt) and provides SNI-based lookup with a self-signed fallback.
//
// Each backend has its own certificate set. A sync from a backend replaces
// only that backend's set, and keeps only certificates for domains inside
// the backend's CertScope; the rest are dropped and counted per backend
// (sentinel_cert_sync_rejected_total). The served map merges the sets as
// described on rebuildLocked.
type CertStore struct {
	mu       sync.RWMutex
	sets     map[string]backendCertSet  // backend ID → its accepted certs
	certs    map[string]tls.Certificate // merged: domain → served cert
	rejected map[string]uint64          // backend ID → certs dropped by scope
	// dropGen counts DropBackend calls per backend, so a sync that was
	// already in flight when its backend was dropped does not put the
	// backend's set back.
	dropGen  map[string]uint64
	fallback tls.Certificate // self-signed fallback

	// scopeFor resolves a backend ID to the domains it is registered
	// for. Nil means no backend is registered for anything.
	scopeFor func(backendID string) CertScope

	lastSync    time.Time
	lastSyncErr error
	syncedCount int
}

// backendCertSet is one backend's accepted certificates and the scope they
// were accepted under.
type backendCertSet struct {
	scope CertScope
	certs map[string]tls.Certificate
}

// NewCertStore creates a CertStore with a self-signed fallback certificate.
func NewCertStore() *CertStore {
	fallback, err := generateSelfSignedCert()
	if err != nil {
		log.Printf("[certsync] warning: failed to generate fallback cert: %v", err)
	}
	return &CertStore{
		sets:     make(map[string]backendCertSet),
		certs:    make(map[string]tls.Certificate),
		rejected: make(map[string]uint64),
		dropGen:  make(map[string]uint64),
		fallback: fallback,
	}
}

// SetScopeResolver sets the function that maps a backend ID to the domains
// it is registered for. Call before the first Sync.
