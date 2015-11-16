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
	}

	// Create L4 app with the appropriate route shape.
	apps["layer4"] = map[string]interface{}{
		"servers": map[string]interface{}{
			L4ServerName: m.buildL4ServerConfig([]interface{}{m.buildCatchallRoute()}),
		},
	}

	if err := m.loadConfig(config); err != nil {
		return fmt.Errorf("failed to load config with L4: %w", err)
	}

	if m.proxyProtocolEnabled() {
		log.Printf("[L4ProxyManager] Activated (PROXY-aware): HTTP moved to %s, L4 owns :443, allow=%v",
			l4HTTPFallbackListen, m.proxyProtocolTrusted)
	} else {
		log.Printf("[L4ProxyManager] Activated: HTTP moved to %s, L4 owns :443", l4HTTPFallbackListen)
	}
	return nil
}

// buildL4ServerConfig returns an L4 server config map using the supplied
// "effective" inner-route list (i.e. the SNI routes plus catchall in their
// natural order). When proxyProtocolEnabled, wraps them in the pattern B
// outer route. When not, returns the flat (legacy) shape.
func (m *L4ProxyManager) buildL4ServerConfig(innerRoutes []interface{}) map[string]interface{} {
	if !m.proxyProtocolEnabled() {
		return map[string]interface{}{
			"listen": []interface{}{":443"},
			"routes": innerRoutes,
		}
	}
	return map[string]interface{}{
		"listen": []interface{}{":443"},
		"routes": []interface{}{
			map[string]interface{}{
				"handle": []interface{}{
					m.buildProxyProtocolHandler(),
					map[string]interface{}{
						"handler": "subroute",
						"routes":  innerRoutes,
					},
				},
			},
		},
	}
}

// buildCatchallRoute returns the catchall (no-match) route that forwards to
// the HTTP server. When proxyProtocolEnabled, the proxy handler emits a
// PROXY v2 header to srv0 so srv0's listener_wrapper can recover the source.
func (m *L4ProxyManager) buildCatchallRoute() map[string]interface{} {
	handler := map[string]interface{}{
		"handler": "proxy",
		"upstreams": []interface{}{
			map[string]interface{}{"dial": []interface{}{l4HTTPFallbackDial}},
		},
	}
	if m.proxyProtocolEnabled() {
		handler["proxy_protocol"] = "v2"
	}
	return map[string]interface{}{
		"handle": []interface{}{handler},
	}
}

// buildProxyProtocolHandler returns the proxy_protocol layer4 handler that
// consumes the PROXY v2 header from connections in the trusted CIDR list.
func (m *L4ProxyManager) buildProxyProtocolHandler() map[string]interface{} {
	allow := make([]interface{}, len(m.proxyProtocolTrusted))
	for i, c := range m.proxyProtocolTrusted {
		allow[i] = c
	}
	return map[string]interface{}{
		"handler": "proxy_protocol",
		"allow":   allow,
		"timeout": "5s",
	}
}

// EnableL4ProxyProtocol records the trusted PROXY senders on this manager and,
// if L4 is already active in Caddy, re-shapes its routes into the wrapped
// (pattern B) topology atomically. Future ActivateL4 calls (e.g. by
// RouteSyncJob when a passthrough route lands in the DB) will also produce
// the wrapped shape.
//
// Idempotent: a second call with the same CIDRs is a no-op when the L4 server
// is already in wrapped form.
func (m *L4ProxyManager) EnableL4ProxyProtocol(trustedCIDRs []string) error {
	if err := m.SetProxyProtocolTrusted(trustedCIDRs); err != nil {
		return err
	}
	if !m.IsL4Active() {
		log.Printf("[L4ProxyManager] PROXY protocol configured (allow=%v); L4 not active yet, will apply on activation", trustedCIDRs)
		return nil
	}
	return m.reshapeL4()
}

// reshapeL4 reads the current L4 server config, extracts the effective inner
// route list (whether the server is currently wrapped or flat), tags the
// catchall with proxy_protocol: "v2", and writes back the wrapped shape.
func (m *L4ProxyManager) reshapeL4() error {
	config, err := m.getFullConfig()
	if err != nil {
		return fmt.Errorf("get full config: %w", err)
	}
	apps := getMapField(config, "apps")
	layer4 := getMapField(apps, "layer4")
	servers := getMapField(layer4, "servers")
	srv := getMapField(servers, L4ServerName)
	if srv == nil {
		return fmt.Errorf("L4 server %q missing", L4ServerName)
	}
	outerRoutes, _ := srv["routes"].([]interface{})
	innerRoutes := effectiveInnerRoutes(outerRoutes)

	// Tag the catchall (last route, no `match`) with proxy_protocol: "v2".
	if len(innerRoutes) > 0 {
		if last, ok := innerRoutes[len(innerRoutes)-1].(map[string]interface{}); ok {
			if _, hasMatch := last["match"]; !hasMatch {
				if hs, ok := last["handle"].([]interface{}); ok {
					for _, h := range hs {
						if hm, ok := h.(map[string]interface{}); ok && hm["handler"] == "proxy" {
							hm["proxy_protocol"] = "v2"
						}
					}
				}
			}
		}
	}

	// Replace the L4 server config with the wrapped shape.
