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
func (cs *CertStore) SetScopeResolver(fn func(backendID string) CertScope) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.scopeFor = fn
}

func (cs *CertStore) scopeOf(backendID string) CertScope {
	cs.mu.RLock()
	fn := cs.scopeFor
	cs.mu.RUnlock()
	if fn == nil {
		return CertScope{}
	}
	return fn(backendID)
}

// Sync fetches certificates from backendID's /certs endpoint and replaces
// that backend's set with the ones inside its registered domains. An empty
// or malformed response, or one with nothing inside the backend's domains,
// leaves the store unchanged.
func (cs *CertStore) Sync(backendID, backendIP string, httpPort int) error {
	cs.mu.RLock()
	gen := cs.dropGen[backendID]
	cs.mu.RUnlock()

	url := fmt.Sprintf("http://%s:%d/certs", backendIP, httpPort)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := newSignedRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("cert sync: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		cs.mu.Lock()
		cs.lastSyncErr = err
		cs.mu.Unlock()
		return fmt.Errorf("cert sync GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("cert sync: unexpected status %d from %s", resp.StatusCode, url)
		cs.mu.Lock()
		cs.lastSyncErr = err
		cs.mu.Unlock()
		return err
	}

	var certsResp gateway.CertsResponse
	if err := json.NewDecoder(resp.Body).Decode(&certsResp); err != nil {
		cs.mu.Lock()
		cs.lastSyncErr = err
		cs.mu.Unlock()
		return fmt.Errorf("cert sync: decode response: %w", err)
	}

	// An empty response never replaces the store. A backend whose Caddy
	// has not issued anything yet (or whose cert dir is missing) answers
	// with an empty list; treating that as "serve nothing" would drop every
	// certificate the sentinel already holds until the next sync. It is
	// not a failure either, so the store is left exactly as it was.
	if len(certsResp.Certs) == 0 {
		log.Printf("[certsync] %s returned no certificates; keeping the %d already held", url, cs.SyncedCount())
		return nil
	}

	scope := cs.scopeOf(backendID)
	newCerts := make(map[string]tls.Certificate, len(certsResp.Certs))
	parsed, rejected := 0, 0
	for _, cp := range certsResp.Certs {
		tlsCert, err := tls.X509KeyPair([]byte(cp.CertPEM), []byte(cp.KeyPEM))
		if err != nil {
			log.Printf("[certsync] warning: failed to parse cert for %s: %v", cp.Domain, err)
			continue
		}
		parsed++
		domain := strings.ToLower(cp.Domain)
		if scope.match(domain) == 0 {
			rejected++
			log.Printf("[certsync] backend %s sent a certificate for %q, which is not among its registered domains; dropped", backendID, cp.Domain)
			continue
		}
		newCerts[domain] = tlsCert
	}
	if rejected > 0 {
		cs.mu.Lock()
		if cs.dropGen[backendID] == gen {
			cs.rejected[backendID] += uint64(rejected)
		}
		cs.mu.Unlock()
	}

	// Entries were sent but none parsed: the response is malformed, and
	// like a decode error it leaves the store untouched.
	if parsed == 0 {
		err := fmt.Errorf("cert sync: none of the %d certificates from %s parsed", len(certsResp.Certs), url)
		cs.mu.Lock()
		cs.lastSyncErr = err
		cs.mu.Unlock()
		return err
	}
	// Every parsed entry was outside the backend's domains: keep its
	// previous set rather than emptying it.
	if len(newCerts) == 0 {
		err := fmt.Errorf("cert sync: all %d certificates from backend %s are outside its registered domains", rejected, backendID)
		if scope.Empty() {
			err = fmt.Errorf("cert sync: backend %s has no registered domains, so none of its %d certificates are served", backendID, rejected)
		}
		cs.mu.Lock()
		cs.lastSyncErr = err
		cs.mu.Unlock()
		return err
	}

	cs.mu.Lock()
	if cs.dropGen[backendID] != gen {
		cs.mu.Unlock()
		log.Printf("[certsync] backend %s was removed during its sync; discarding the result", backendID)
		return nil
	}
	cs.sets[backendID] = backendCertSet{scope: scope, certs: newCerts}
	cs.rebuildLocked()
	cs.lastSync = time.Now()
	cs.lastSyncErr = nil
	cs.mu.Unlock()

	return nil
}

// DropBackend removes backendID's certificate set, e.g. when its tunnel
// disconnects. Domains it supplied fall back to the next-ranked backend's
// certificate, or to the self-signed fallback. Its rejection series is
// removed too, so /metrics carries series only for current backends.
func (cs *CertStore) DropBackend(backendID string) {
	cs.mu.Lock()
