package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// L4ServerName is the Caddy L4 server name for TLS passthrough routing
const L4ServerName = "tls_passthrough"

// l4HTTPFallbackListen is the listen address the HTTP server moves to when L4 is active.
// L4 takes over :443 and proxies non-matching SNI to this address.
const l4HTTPFallbackListen = ":8443"

// l4HTTPFallbackDial is the dial address L4 uses to reach the HTTP server fallback.
const l4HTTPFallbackDial = "localhost:8443"

// L4Route represents a TLS passthrough route (our domain model)
type L4Route struct {
	SNI          string `json:"sni"`
	UpstreamIP   string `json:"upstream_ip"`
	UpstreamPort int    `json:"upstream_port"`
}

// L4ProxyManager manages Caddy L4 (TLS passthrough) routes via the admin API.
// L4 is activated lazily — only when TLS passthrough routes exist in the database.
// Activation uses Caddy's /load endpoint for atomic config replacement.
//
// When proxyProtocolTrusted is non-empty, the L4 server is structured per the
// sandbox-tier-1-verified pattern B: a single outer route whose handlers are
// (proxy_protocol, subroute), with the catchall inside the subroute tagged
// with proxy_protocol: "v2" so caddy-l4 emits a PROXY header to srv0. SNI
// passthrough routes live inside the subroute too — getRoutes/putRoutes
// detect the wrapping and operate on the inner route list, so the existing
// AddL4Route / RemoveL4Route / ListL4Routes flows continue to work without
// changes.
type L4ProxyManager struct {
	caddyAdminURL        string
	httpClient           *http.Client
	proxyProtocolTrusted []string // empty = no PROXY-protocol awareness
}

// NewL4ProxyManager creates a new L4 proxy manager.
func NewL4ProxyManager(caddyAdminURL string) *L4ProxyManager {
	return &L4ProxyManager{
		caddyAdminURL: caddyAdminURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SetProxyProtocolTrusted records the CIDRs from which the L4 server will
// accept PROXY v2 headers. Once set, every subsequent ActivateL4 produces the
// wrapped pattern-B shape (and EnableL4ProxyProtocol can re-shape an already
// active server).
//
// trustedSenderCIDRs MUST NOT be empty or wildcard.
func (m *L4ProxyManager) SetProxyProtocolTrusted(trustedSenderCIDRs []string) error {
	if len(trustedSenderCIDRs) == 0 {
		return fmt.Errorf("L4ProxyManager.SetProxyProtocolTrusted: trustedSenderCIDRs must not be empty")
	}
	for _, c := range trustedSenderCIDRs {
		if c == "0.0.0.0/0" || c == "::/0" {
			return fmt.Errorf("L4ProxyManager.SetProxyProtocolTrusted: refusing wildcard CIDR %q", c)
		}
	}
	m.proxyProtocolTrusted = trustedSenderCIDRs
	return nil
}

// proxyProtocolEnabled reports whether the manager has been configured to
// produce the wrapped shape. Used by ActivateL4 + helpers.
func (m *L4ProxyManager) proxyProtocolEnabled() bool {
	return len(m.proxyProtocolTrusted) > 0
}

// IsL4Active checks if the L4 layer4 app is configured in Caddy
// by looking for the specific tls_passthrough server.
func (m *L4ProxyManager) IsL4Active() bool {
	url := fmt.Sprintf("%s/config/apps/layer4/servers/%s", m.caddyAdminURL, L4ServerName)
	resp, err := m.httpClient.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, _ := io.ReadAll(resp.Body)
	trimmed := strings.TrimSpace(string(body))
	return len(trimmed) > 0 && trimmed != "null"
}

// ActivateL4 atomically activates L4 SNI routing on :443.
//
// It reads the full Caddy config, moves the HTTP server from :443 to :8443
// (with tls_connection_policies for TLS on non-standard port), creates the
// L4 app on :443 with a catch-all route that proxies to localhost:8443,
// and atomically applies the config via POST /load.
//
// When the manager has been configured with proxyProtocolTrusted (via
// SetProxyProtocolTrusted or EnableL4ProxyProtocol), the L4 server is
// produced in the sandbox-tier-1-verified pattern B shape: a single outer
// route whose handlers are (proxy_protocol, subroute), with the catchall
// inside the subroute tagged with `proxy_protocol: "v2"` so caddy-l4
// re-emits a PROXY header to srv0. SNI passthrough routes (added later via
// AddL4Route) live inside the same subroute.
//
// This is idempotent — if L4 is already active, it returns nil.
func (m *L4ProxyManager) ActivateL4() error {
	if m.IsL4Active() {
		return nil
	}

	config, err := m.getFullConfig()
	if err != nil {
		return fmt.Errorf("failed to get full config: %w", err)
	}

	apps := getMapField(config, "apps")
	if apps == nil {
		return fmt.Errorf("no apps in config")
	}

	// Modify HTTP server: change :443 to :8443 and add tls_connection_policies
	if err := m.moveHTTPServerOff443(apps); err != nil {
		return fmt.Errorf("failed to modify HTTP server: %w", err)
