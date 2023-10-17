package server

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/config"
	metricsPackage "github.com/footprintai/containarium/internal/metrics"
	"github.com/footprintai/containarium/internal/metrics/cloudexport"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// PeerClient represents a connection to a remote containarium daemon.
type PeerClient struct {
	ID         string
	Addr       string // host:port of the remote daemon's REST API
	Pool       string // pool tag from the peer's --pool registration (empty if untagged)
	Healthy    bool
	LastSeenAt time.Time // Timestamp of last successful health check
	client     *http.Client
	scheme     string // "http" pre-Phase-0.5; "https" once PeerPool.BootstrapPKI succeeds

	// Cached system info from last discovery poll
	CachedHostname       string
	CachedOS             string
	CachedVersion        string
	CachedContainerCount int32

	// Cached CPU capacity for placement ranking (#1029 direction 2). Refreshed
	// on the discovery cadence when capacity ranking is enabled; read by the
	// placement ranker. All three are written under PeerPool.mu and must be
	// read under it too (PickLeastCommittedInPool does). CapacityKnown gates
	// use: until the first successful fetch the peer is treated as
	// unknown-capacity and ranked after peers whose load we do know.
	CachedPhysicalCores  float64
	CachedCommittedCores float64
	CapacityKnown        bool
}

// commitRatio reports this peer's CPU overcommit ratio (committed cores /
// physical cores) and whether it is known. Unknown when no successful capacity
// fetch has happened yet or the peer reports no cores. Callers must hold the
// owning PeerPool's lock (fields are written under it).
func (pc *PeerClient) commitRatio() (float64, bool) {
	if !pc.CapacityKnown || pc.CachedPhysicalCores <= 0 {
		return 0, false
	}
	return pc.CachedCommittedCores / pc.CachedPhysicalCores, true
}

// urlScheme returns "http" or "https" — defaults to "http" for
// backwards compatibility during the Phase 0.5 rollout.
func (pc *PeerClient) urlScheme() string {
	if pc.scheme == "" {
		return "http"
	}
	return pc.scheme
}

// PeerPool manages connections to remote containarium daemon peers.
// The primary daemon uses this to fan out API calls to other backends.
type PeerPool struct {
	mu    sync.RWMutex
	peers map[string]*PeerClient

	// Auto-discovery from sentinel
	sentinelURL    string
	pool           string // if non-empty, only peers tagged with this pool are discovered
	discoveryStop  chan struct{}
	localBackendID string // this daemon's backend ID

	// pki holds the daemon's leaf cert + pinned peer-CA for Phase
	// 0.5 HTTPS peer-to-peer. Nil when no CA is configured —
	// peer.go then falls back to plain HTTP (pre-0.5 behavior).
	pki *peerPKI

	// plainHTTPLogOnce makes the "peer mTLS not enabled" notice a
	// one-time line (#3079).
	plainHTTPLogOnce sync.Once

	// Capacity-aware placement ranking (#1029 direction 2). When
	// capacityRanking is true and capacityTokenFn is set, the discovery loop
	// also refreshes each healthy peer's CPU commitment, and
	// PickLeastCommittedInPool ranks candidates by it. Both are set once via
	// EnableCapacityRanking; unset leaves placement at first-healthy.
	// capacityTokenFn returns the renewing internal admin token (see
	// serviceTokenSource) — a func, not a stored string, so the daemon can't
	// fall off a one-shot token after 30 days.
	capacityRanking bool
	capacityTokenFn func() (string, error)
}

// CapacityRankingEnabled reports whether commitment-ranked placement is active.
func (p *PeerPool) CapacityRankingEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.capacityRanking
}

// EnableCapacityRanking turns on per-peer CPU-commitment tracking and
// commitment-ranked placement (#1029 direction 2). tokenFn returns the
// admin-scoped internal token used to read each peer's system-info (physical
// cores) and container list (committed cores) — both admin-only endpoints —
// and is expected to renew the token itself (serviceTokenSource). A no-op if
// tokenFn is nil. Idempotent; call once during setup.
func (p *PeerPool) EnableCapacityRanking(tokenFn func() (string, error)) {
	if tokenFn == nil {
		return
	}
	p.mu.Lock()
	p.capacityRanking = true
	p.capacityTokenFn = tokenFn
	p.mu.Unlock()
}

// NewPeerPool creates a new peer pool.
// If sentinelURL is provided, it will auto-discover tunnel backends.
// localBackendID is used to tag local containers.
// pool, when non-empty, scopes auto-discovery to peers tagged with that pool only;
// pass "" to see all peers regardless of tag.
func NewPeerPool(localBackendID string, sentinelURL string, staticPeers []string, pool string) *PeerPool {
	p := &PeerPool{
		peers:          make(map[string]*PeerClient),
		sentinelURL:    sentinelURL,
		pool:           pool,
		localBackendID: localBackendID,
	}

	// Add static peers. They start on plain HTTP; BootstrapPKI
	// upgrades them in place once the daemon fetches its cert.
	for _, addr := range staticPeers {
		p.peers[addr] = &PeerClient{
			ID:   addr,
			Addr: addr,
			client: &http.Client{
				Timeout: 30 * time.Second,
			},
		}
	}

	return p
}

// BootstrapPKI fetches a leaf cert from the sentinel and wires it
// into every peer client in the pool. After this returns
// successfully, peer-to-peer calls use HTTPS with CA pinning
// instead of plain HTTP. Safe to call when no PKI is configured —
// it just returns nil and the pool keeps using HTTP.
//
// Errors are NOT fatal; the caller (dual_server.go) logs them and
// falls back to HTTP. Operators see the message clearly in the
// startup log.
func (p *PeerPool) BootstrapPKI(ctx context.Context) error {
	if p.sentinelURL == "" {
		return nil
	}
	secret := loadSentinelHMACSecret()
	if len(secret) < auth.SentinelMinSecretLen {
		// No HMAC secret → no way to authenticate the cert request.
		// Stay on HTTP; the audit warning is logged in discover().
		return nil
	}
	if p.localBackendID == "" {
		return fmt.Errorf("local backend ID is empty; cannot request peer cert")
	}

	pki, err := FetchPeerPKI(ctx, p.sentinelURL, p.localBackendID, secret)
	if err != nil {
		return fmt.Errorf("fetch peer PKI from sentinel: %w", err)
	}

	p.mu.Lock()
	p.pki = pki
	// Rebuild every existing peer client with the TLS-aware HTTP
	// client so subsequent calls use HTTPS pinning.
	for _, pc := range p.peers {
		pc.client = buildPeerHTTPClient(pki)
		pc.scheme = "https"
	}
	p.mu.Unlock()

	log.Printf("[peer-pki] bootstrap complete; %d peer client(s) upgraded to HTTPS", len(p.peers))
	return nil
}

// PeerMTLSEnabled reports whether this daemon's peer calls use the
// sentinel-issued leaf cert and pinned peer CA. False when the bootstrap
// was skipped, failed, or has not run. Nil-safe.
func (p *PeerPool) PeerMTLSEnabled() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pki != nil
}

// PeerTransportRelevant reports whether this daemon has any peer calls to
// describe: a sentinel URL (discovery can add peers at any time) or at least
// one peer. Evaluated at read time so peers added later are picked up. A
// standalone daemon (no sentinel, no peers) makes no peer calls. Nil-safe.
func (p *PeerPool) PeerTransportRelevant() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sentinelURL != "" || len(p.peers) > 0
}

// PeerMTLSState returns (enabled, relevant) for the metric probe.
func (p *PeerPool) PeerMTLSState() (enabled, relevant bool) {
	return p.PeerMTLSEnabled(), p.PeerTransportRelevant()
}

// peerTransportOf maps a pool to the PeerTransport reported in
// SystemInfo. A nil pool is UNSPECIFIED (no peer calls to describe).
func peerTransportOf(p *PeerPool) pb.PeerTransport {
	if !p.PeerTransportRelevant() {
		return pb.PeerTransport_PEER_TRANSPORT_UNSPECIFIED
	}
	if p.PeerMTLSEnabled() {
		return pb.PeerTransport_PEER_TRANSPORT_MTLS
	}
	return pb.PeerTransport_PEER_TRANSPORT_HTTP
}

// LogPlainHTTPOnce logs, at most once per pool, that peer mTLS is not
// enabled and daemon-to-peer calls use plain HTTP. reason is the bootstrap
// error, or nil when the bootstrap was skipped. It is a supported
// configuration, so this is a notice, not an error (#3079). It states only
// the peer-call transport; it says nothing about other transports such as
// the sentinel-backend tunnel. No-op when peer mTLS is active or the daemon has no sentinel and no peers.
func (p *PeerPool) LogPlainHTTPOnce(reason error) {
	if p.PeerMTLSEnabled() || !p.PeerTransportRelevant() {
		return
	}
	p.plainHTTPLogOnce.Do(func() {
		if reason != nil {
			log.Printf("[peer-pki] peer mTLS not enabled: peer-to-peer calls use plain HTTP (%v). Visible as peer_transport in system info and containarium.peer.mtls.enabled", reason)
			return
		}
		log.Printf("[peer-pki] peer mTLS not enabled: peer-to-peer calls use plain HTTP. Visible as peer_transport in system info and containarium.peer.mtls.enabled")
	})
}

// buildPeerHTTPClient returns an http.Client wired to present the
// daemon's leaf cert and verify peer / sentinel server certs
// against the pinned CA. When `pki` is nil, returns a plain client
// with no TLS customization (pre-0.5 behavior).
//
// GetClientCertificate is a closure over the *peerPKI value, so a
// renewal that calls pki.replace() takes effect on the very next
// TLS handshake — no need to rebuild any http.Client. The RootCAs
// pool is similarly read on each handshake via VerifyPeerCertificate
// indirection through `pki.CACertPool()`. We pull the pool once at
// build time because Go's tls.Config has no RootCAs callback;
// callers should rebuild the client if the CA itself rotates,
// which only happens when the operator replaces ca.key on the
// sentinel (rare).
func buildPeerHTTPClient(pki *peerPKI) *http.Client {
	if pki == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    pki.CACertPool(),
				MinVersion: tls.VersionTLS12,
				GetClientCertificate: func(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
					cert := pki.ClientCertificate()
					return &cert, nil
				},
			},
		},
	}
}

// StartCertRenewal launches a background goroutine that watches
// the daemon's leaf cert and re-issues from the sentinel when the
// remaining lifetime drops to 1/3 of the original TTL. Safe to
// call when no PKI is configured — it's a no-op. Exits when ctx
// is cancelled.
//
// Renewal calls pki.replace() in place; existing http.Clients
// inherit the new leaf at the next TLS handshake (see
// buildPeerHTTPClient and its GetClientCertificate callback).
func (p *PeerPool) StartCertRenewal(ctx context.Context) {
	if p.pki == nil {
		return
	}
	go func() {
		// Check every minute; the renewal threshold is much
		// coarser than that, so a 1-minute tick is plenty.
		// Avoid sub-minute ticks because the sentinel is the
		// rate-limiting factor on issuance and we don't want a
		// fleet-wide stampede.
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		log.Printf("[peer-pki] renewal watcher started; next cert expires %s", p.pki.Expiry().Format(time.RFC3339))
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := p.maybeRenewCert(ctx); err != nil {
					log.Printf("[peer-pki] renewal failed (will retry): %v", err)
				}
			}
		}
	}()
}

// maybeRenewCert checks the current leaf's remaining lifetime
// against a 1/3-TTL threshold and re-issues if we're past it.
// Idempotent: if the cert isn't due for renewal yet, this is a
// no-op. Returns an error only when the sentinel call itself
// fails — the caller's retry loop handles transient failures.
func (p *PeerPool) maybeRenewCert(ctx context.Context) error {
	if p.pki == nil {
		return nil
	}
	expiry := p.pki.Expiry()
	if expiry.IsZero() {
		return nil
	}
	remaining := time.Until(expiry)
	// Renew when remaining lifetime is below 1/3 of the leaf TTL.
	// We use pki.DefaultLeafExpiry as the canonical full TTL —
	// the daemon doesn't know the sentinel's configured value,
	// but the default is the only one shipped today and gives a
	// reasonable threshold either way.
	threshold := 7 * 24 * time.Hour / 3 // ≈ 2d 8h for the 7-day default
	if remaining > threshold {
		return nil
	}
	log.Printf("[peer-pki] cert remaining=%s < threshold=%s, renewing from sentinel", remaining.Round(time.Second), threshold)

	secret := loadSentinelHMACSecret()
	if len(secret) < auth.SentinelMinSecretLen {
		return fmt.Errorf("HMAC secret unavailable")
	}
	fresh, err := FetchPeerPKI(ctx, p.sentinelURL, p.localBackendID, secret)
	if err != nil {
		return err
	}
	// Swap leaf + CA + expiry atomically. Existing http.Clients
	// pick up the new leaf on their next TLS handshake.
	p.pki.replace(fresh.ClientCertificate(), fresh.CACertPool(), fresh.CACertPEM(), fresh.Expiry())
	log.Printf("[peer-pki] renewed; new expiry %s (in %s)", fresh.Expiry().Format(time.RFC3339), time.Until(fresh.Expiry()).Round(time.Second))
	return nil
}

// StartDiscovery starts background auto-discovery of peers from the sentinel.
func (p *PeerPool) StartDiscovery(ctx context.Context) {
	if p.sentinelURL == "" {
		return
	}

	p.discoveryStop = make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		// Initial discovery
		p.discover()

		for {
			select {
			case <-ctx.Done():
				return
			case <-p.discoveryStop:
				return
			case <-ticker.C:
				p.discover()
			}
		}
	}()
	log.Printf("[peers] auto-discovery started (sentinel: %s)", p.sentinelURL)
}

// loadSentinelHMACSecret returns the sentinel-shared HMAC secret
// (CONTAINARIUM_SENTINEL_AUTH_SECRET) used to verify the signed
// /sentinel/peers response. Cached on first call so we don't read
// the env var on every discovery tick. An empty return means the
// secret is unset.
var (
	sentinelHMACSecretOnce sync.Once
	sentinelHMACSecret     []byte
)

func loadSentinelHMACSecret() []byte {
	sentinelHMACSecretOnce.Do(func() {
		if raw := os.Getenv(config.EnvSentinelAuthSecret); raw != "" {
			sentinelHMACSecret = []byte(raw)
		}
	})
	return sentinelHMACSecret
}

// sentinelPubKey caches the sentinel's ed25519 public key
// (CONTAINARIUM_SENTINEL_PUBLIC_KEY, #688) used to verify the signed
// /sentinel/peers response. Nil when unset or invalid.
var (
	sentinelPubKeyOnce sync.Once
	sentinelPubKey     ed25519.PublicKey
)

func loadSentinelPublicKey() ed25519.PublicKey {
	sentinelPubKeyOnce.Do(func() {
		if raw := strings.TrimSpace(os.Getenv(config.EnvSentinelPublicKey)); raw != "" {
			if pub, err := auth.ParseSentinelPublicKey(raw); err != nil {
				log.Printf("[peers] CONTAINARIUM_SENTINEL_PUBLIC_KEY invalid (%v) — falling back to HMAC for discovery verification", err)
			} else {
				sentinelPubKey = pub
			}
		}
	})
	return sentinelPubKey
}

// discover fetches peer list from sentinel's /sentinel/peers endpoint.
// When p.pool is non-empty, ?pool=<name> is appended so the sentinel returns
// only peers tagged with that pool.
//
// The response body is HMAC-signed by the sentinel (see
// auth.SignSentinelResponse + sentinel.PeersHandler). Without a
// valid signature the daemon refuses to update its peer map — a
// compromised sentinel or active MITM cannot inject attacker peer
// URLs that this daemon would proxy container traffic through.
// Fixes finding C-CRIT-2.
func (p *PeerPool) discover() {
	url := p.sentinelURL + "/sentinel/peers"
	if p.pool != "" {
		url += "?pool=" + neturl.QueryEscape(p.pool)
	}
	// Use the PKI-aware client when the daemon has a CA pinned —
	// otherwise the bare client can't verify the sentinel's
	// self-signed server cert on the HTTPS port.
	var client *http.Client
	if p.pki != nil {
		client = buildPeerHTTPClient(p.pki)
		client.Timeout = 5 * time.Second
	} else {
		client = &http.Client{Timeout: 5 * time.Second}
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		log.Printf("[peers] discovery failed: %v", err)
		return
	}
	// The sentinel returns pool tags (and honours ?pool=) only to a caller
	// with a valid daemon signature (#3119). Without a secret we still
	// discover peers, but they arrive untagged.
	if secret := loadSentinelHMACSecret(); len(secret) >= auth.SentinelMinSecretLen {
		auth.SignSentinelRequest(req, secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[peers] discovery failed: %v", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	// Verify the sentinel signed the response. Fail-closed: an
	// unsigned or tampered response leaves the peer map unchanged
	// rather than silently trusting unauthenticated discovery data.
	// Dual-accept (#688): ed25519 (CONTAINARIUM_SENTINEL_PUBLIC_KEY) and/or
	// the legacy HMAC (CONTAINARIUM_SENTINEL_AUTH_SECRET).
	verifier := auth.NewSentinelVerifier(loadSentinelPublicKey(), loadSentinelHMACSecret())
