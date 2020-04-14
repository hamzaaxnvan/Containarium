package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultCaddyServerName is the default Caddy server name used in Caddyfile configs
const DefaultCaddyServerName = "srv0"

// ProxyManager manages Caddy reverse proxy configuration
type ProxyManager struct {
	caddyAdminURL string
	baseDomain    string
	serverName    string // Caddy server name (default: "srv0")
	httpClient    *http.Client
	// dnsChallenge, when non-nil, makes provisioned policies solve ACME
	// DNS-01 (required for wildcard certs). Nil keeps Caddy's default
	// HTTP-01 + TLS-ALPN-01. Configured via WithDNSChallenge /
	// DNSChallengeFromEnv. See issue #378.
	dnsChallenge *CaddyACMEChallenges
	// proxyProtocolTrusted records the CIDRs passed to the last successful
	// EnableProxyProtocol, so the self-healing reconcile (EnsureBaseConfig)
	// can re-install the PROXY listener wrappers after the bundled Caddy
	// reverts to its stub Caddyfile. Empty = PROXY protocol not enabled. #400.
	proxyProtocolTrusted []string
	// clientIPHeaders / trustedProxyCIDRs record the CDN-fronted real-client-IP
	// configuration (#1829): the headers Caddy reads the original client IP
	// from (e.g. Cf-Connecting-Ip) and the extra CIDRs (the CDN's published
	// ranges) unioned into trusted_proxies so those headers are honored only
	// from the CDN's own networks. Remembered so EnsureBaseConfig re-applies
	// them after a stub revert, exactly like proxyProtocolTrusted.
	clientIPHeaders   []string
	trustedProxyCIDRs []string
	// wafEnabled prepends the coraza-caddy WAF handler to every HTTP route's
	// handler chain (Tier 3 PR-3, #662). Off by default — when false, routes are
	// byte-identical to before. Requires the Caddy binary built with the coraza
	// module. wafDirectives is the SecLang the engine loads (CRS + engine mode).
	wafEnabled    bool
	wafDirectives string
	// byocIngressAddr, when non-empty, adds one extra plaintext listener to the
	// edge server (e.g. "127.0.0.1:8081") that serves the SAME Host-matched
	// routes as :443 but WITHOUT TLS — the sentinel-terminate BYOC public HTTP
	// ingress data path (#733 slice 3). On a BYOC host the sentinel terminates
	// TLS with the wildcard it holds and plaintext-forwards over the tunnel to
	// this listener, which Host-routes to the box; the host needs no cert.
	//
	// MUST be loopback-scoped ("127.0.0.1:<port>") so it is reachable only via
	// the tunnel client (which dials 127.0.0.1:<port>) and never from the LAN —
	// it serves tenant routes in the clear. Empty = disabled (default): region
	// hosts and non-BYOC deployments are byte-identical to before. Because it is
	// not Caddy's http_port (80), automatic-HTTPS redirects don't apply — the
	// listener serves the reverse_proxy routes directly as plaintext HTTP.
	byocIngressAddr string
}

// RouteProtocol represents the protocol type for a route
type RouteProtocol string

const (
	// RouteProtocolHTTP is for standard HTTP/1.1 and HTTP/2 routes
	RouteProtocolHTTP RouteProtocol = "http"
	// RouteProtocolGRPC is for gRPC routes (requires HTTP/2)
	RouteProtocolGRPC RouteProtocol = "grpc"
	// RouteProtocolTLSPassthrough is for TLS passthrough routes (SNI-based, no TLS termination)
	RouteProtocolTLSPassthrough RouteProtocol = "tls_passthrough"
)

// Route represents a proxy route configuration (our domain model)
type Route struct {
	Subdomain    string        `json:"subdomain"`
	FullDomain   string        `json:"full_domain"`
	UpstreamIP   string        `json:"upstream_ip"`
	UpstreamPort int           `json:"upstream_port"`
	Protocol     RouteProtocol `json:"protocol,omitempty"` // "http" or "grpc", defaults to "http"
}

// caddyRouteJSON is used for JSON marshaling routes to Caddy API. Handle is the
// CaddyHandler interface so the chain can carry a WAF handler before
// reverse_proxy (#662 PR-3); a reverse_proxy handler marshals identically either
// way, so a WAF-disabled route is byte-for-byte what it was.
type caddyRouteJSON struct {
	ID     string            `json:"@id,omitempty"`
	Match  []CaddyMatchTyped `json:"match,omitempty"`
	Handle []CaddyHandler    `json:"handle,omitempty"`
}

// caddyRouteRaw is used for unmarshaling routes from Caddy API
// Caddy can return various handler types, so we use json.RawMessage
type caddyRouteRaw struct {
	ID     string            `json:"@id,omitempty"`
	Match  []CaddyMatchTyped `json:"match,omitempty"`
	Handle []json.RawMessage `json:"handle,omitempty"`
}

// NewProxyManager creates a new proxy manager
func NewProxyManager(caddyAdminURL, baseDomain string) *ProxyManager {
	return &ProxyManager{
		caddyAdminURL: caddyAdminURL,
		baseDomain:    baseDomain,
		serverName:    DefaultCaddyServerName,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// WithDNSChallenge configures the manager to provision certificates via ACME
// DNS-01, which is required for wildcard subjects and sidesteps the HTTP-01
// redirect failure mode. Nil keeps the default HTTP-01 + TLS-ALPN-01 path.
// Returns the manager for chaining. See DNSChallengeFromEnv and issue #378.
func (p *ProxyManager) WithDNSChallenge(dns *CaddyACMEChallenges) *ProxyManager {
	p.dnsChallenge = dns
	return p
}

// HasDNSChallenge reports whether the manager is configured to solve ACME
// DNS-01 — i.e. whether a wildcard certificate can be issued (HTTP-01 /
// TLS-ALPN-01 cannot). Callers use this to decide whether to provision a
// `*.<base-domain>` wildcard for per-region/subdomain endpoints. See #389.
func (p *ProxyManager) HasDNSChallenge() bool {
	return p.dnsChallenge != nil
}

// WithWAF enables the coraza-caddy WAF on every HTTP route this manager programs
// (#662 PR-3): the WAF handler is prepended to the handler chain so Coraza
// evaluates the request (CRS + virtual-patch rules in directives) before it
// reaches the upstream. Requires the Caddy binary built with the coraza module.
// directives is the SecLang to load (see DefaultWAFDirectives — enforce vs
// detection-only). Off by default; returns the manager for chaining.
func (p *ProxyManager) WithWAF(directives string) *ProxyManager {
	p.wafEnabled = true
	p.wafDirectives = directives
	return p
}

// WAFEnabled reports whether ingress WAF inspection is on.
func (p *ProxyManager) WAFEnabled() bool { return p.wafEnabled }

// WithBYOCIngress adds a loopback-scoped plaintext ingress listener to the edge
// server for the sentinel-terminate BYOC public-HTTP-ingress path (#733). addr
// should be "127.0.0.1:<port>"; an empty addr is a no-op (default: disabled).
// Returns the manager for chaining. See the byocIngressAddr field doc.
func (p *ProxyManager) WithBYOCIngress(addr string) *ProxyManager {
	p.byocIngressAddr = strings.TrimSpace(addr)
	return p
}

// BYOCIngressAddr returns the configured plaintext ingress listener address
// (empty when disabled).
func (p *ProxyManager) BYOCIngressAddr() string { return p.byocIngressAddr }

// listenAddrs returns the edge server's listen set: always :80 and :443, plus
// the BYOC plaintext ingress listener when configured (#733 slice 3). Centralised
// so every place that (re)builds the server config — createHTTPApp and
// createServerConfig, both reached by the #400 self-heal rebuild — agrees, so a
// Caddy revert-and-rebuild doesn't silently drop the ingress listener.
func (p *ProxyManager) listenAddrs() []string {
	addrs := []string{":80"}
	// #1743: when L4ProxyManager has activated SNI passthrough on this
	// host, it has already moved the HTTP server off :443 onto :8443 (see
	// l4_proxy.go's moveHTTPServerOff443) so the layer4 app can own :443
	// exclusively — it has to see every connection to inspect SNI before
	// routing. Including :443 here anyway doesn't just leave it unused: on
	// the next EnsureServerConfig/addMissingListenAddrs reconcile (a plain
	// daemon restart is enough), it gets ADDED BACK to the HTTP server's
	// listen list, so both servers end up bound to :443 and the kernel
	// load-balances new connections between them — roughly half land on
	// the HTTP server, which has no route for an SNI-passthrough-only
	// hostname, and the client sees a bare TLS handshake failure. Checking
	// live L4 state here, rather than caching a flag that could drift
	// from what L4ProxyManager (a separate, independently-reconciled
	// manager) actually has live, is what keeps this correct across a
	// restart.
	if !p.l4Active() {
		addrs = append(addrs, ":443")
	}
	if p.byocIngressAddr != "" {
		addrs = append(addrs, p.byocIngressAddr)
	}
	return addrs
}

// l4Active reports whether this host's Caddy currently has an active
// layer4 SNI-passthrough server (#1743) by asking L4ProxyManager — the one
// piece of code that actually knows the shape of that app — rather than
// re-deriving the check here. ProxyManager and L4ProxyManager are separate,
// independently-activated managers over the same Caddy admin API with no
// shared state, so this queries the live config each time instead of
// caching a flag that could go stale the moment L4 is activated or
// deactivated out from under this ProxyManager.
//
// Fails as "not active" (the pre-existing behavior: :443 stays in the HTTP
// server's own listen list) on any admin-API error — the same posture
// EnsureServerConfig's own reachability checks already take elsewhere in
// this file.
func (p *ProxyManager) l4Active() bool {
	return NewL4ProxyManager(p.caddyAdminURL).IsL4Active()
}

// SetServerName sets the Caddy server name (useful when Caddy uses a custom server name)
func (p *ProxyManager) SetServerName(name string) {
	p.serverName = name
}

// AddRoute adds a new HTTP route to Caddy
// If subdomain contains the base domain, it's used as-is; otherwise base domain is appended
func (p *ProxyManager) AddRoute(subdomain, containerIP string, port int) error {
	return p.addRouteWithProtocol(subdomain, containerIP, port, RouteProtocolHTTP)
}

// AddGRPCRoute adds a new gRPC route to Caddy with HTTP/2 transport
// gRPC requires HTTP/2 (h2c for cleartext connection to backend)
func (p *ProxyManager) AddGRPCRoute(subdomain, containerIP string, port int) error {
	return p.addRouteWithProtocol(subdomain, containerIP, port, RouteProtocolGRPC)
}

// addRouteWithProtocol adds a route with the specified protocol type
func (p *ProxyManager) addRouteWithProtocol(subdomain, containerIP string, port int, protocol RouteProtocol) error {
	fullDomain := subdomain
	routeID := subdomain

	// Determine whether to append the base domain.
	// If the input is a simple subdomain (no dots), append baseDomain.
	// If it already contains baseDomain as suffix, extract the subdomain part.
	// If it's a fully-qualified domain that is NOT a subdomain of baseDomain
	// (e.g., "api.example.com" when baseDomain is "<cluster>.example.com"),
	// use it as-is — it's an independent domain.
	if p.baseDomain != "" {
		if strings.HasSuffix(subdomain, "."+p.baseDomain) || strings.HasSuffix(subdomain, p.baseDomain) {
			// Already contains base domain — extract subdomain for route ID
			routeID = strings.TrimSuffix(subdomain, "."+p.baseDomain)
			routeID = strings.TrimSuffix(routeID, p.baseDomain)
		} else if !strings.Contains(subdomain, ".") {
			// Simple subdomain (no dots) — append base domain
			fullDomain = fmt.Sprintf("%s.%s", subdomain, p.baseDomain)
		}
		// Otherwise: it's a FQDN that is not a subdomain of baseDomain — use as-is
	}

	// Create handler based on protocol
	handler := CaddyReverseProxyHandler{
		Handler: "reverse_proxy",
		Upstreams: []CaddyUpstreamTyped{
			{Dial: fmt.Sprintf("%s:%d", containerIP, port)},
		},
	}

	// Add HTTP/2 transport for gRPC
	if protocol == RouteProtocolGRPC {
		handler.Transport = NewGRPCTransport()
	}

	route := caddyRouteJSON{
		ID: routeID, // Use subdomain as route ID for easy removal
		Match: []CaddyMatchTyped{
			{Host: []string{fullDomain}},
		},
		// Prepend the coraza WAF handler when enabled, so it inspects before
		// reverse_proxy; a no-op (unchanged chain) when disabled (#662 PR-3).
		Handle: PrependWAF([]CaddyHandler{handler}, p.wafEnabled, p.wafDirectives),
	}

	// Serialize route to JSON
	routeJSON, err := json.Marshal(route)
	if err != nil {
		return fmt.Errorf("failed to marshal route: %w", err)
	}

	// Add route via Caddy API
	url := fmt.Sprintf("%s/config/apps/http/servers/%s/routes", p.caddyAdminURL, p.serverName)
	req, err := http.NewRequest("POST", url, bytes.NewReader(routeJSON))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to add route: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("caddy returned error (status %d): %s", resp.StatusCode, string(body))
	}

	// Provision TLS certificate for the domain.
	//
	// The route itself is already live at this point, so a TLS failure isn't
	// returned as an error — doing so would tell the caller the route wasn't
	// added, which is false, and invite a rollback of a working route.
	//
	// But it must not vanish either (#1585). This used to be a bare
	// fmt.Printf to stdout: no timestamp, no prefix, invisible to the log
	// scraping every other line in this file supports, and the caller
	// (expose_port) reported unqualified success while HTTPS for the
	// hostname was permanently dead.
	//
	// Two things make that safe now: this logs like everything else, and
	// RouteSyncJob re-checks TLS subjects on every tick (#1584), so a failure
	// here is repaired on the next sync rather than persisting forever.
	if err := p.ProvisionTLS(fullDomain); err != nil {
		log.Printf("[ProxyManager] route for %s is live but TLS provisioning failed: %v — "+
			"HTTPS will fail for this host until the next route-sync tick reconciles it (#1584/#1585)",
			fullDomain, err)
	}

	return nil
}

// RemoveTLSSubject strips `domain` from every TLS automation policy's
// `subjects` list. Used by the container-delete cascade: once a container
// is gone, Caddy should stop trying to ACME-renew its hostname's cert
// (otherwise it'll keep hitting Let's Encrypt with no upstream to
// challenge, slowly burning rate-limit budget).
//
// No-op if `domain` isn't in any subjects list. Leaves an empty policy
// in place if `domain` was the only subject — that's harmless (Caddy
// just has nothing to do for it) and avoids the complexity of deciding
// whether to delete the entire policy entry (which might be the only
// reference to the ACME issuers config).
func (p *ProxyManager) RemoveTLSSubject(domain string) error {
	url := fmt.Sprintf("%s/config/apps/tls/automation/policies", p.caddyAdminURL)
	resp, err := p.httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("get TLS policies: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest {
		// No TLS app configured; nothing to remove.
		return nil
	}

	var policies []CaddyTLSAutomationPolicy
	body, _ := io.ReadAll(resp.Body)
	if len(bytes.TrimSpace(body)) == 0 || string(bytes.TrimSpace(body)) == "null" {
		return nil
	}
	if err := json.Unmarshal(body, &policies); err != nil {
		return fmt.Errorf("decode TLS policies: %w", err)
	}

	changed := false
	for i := range policies {
		filtered := policies[i].Subjects[:0]
		for _, s := range policies[i].Subjects {
			if s == domain {
				changed = true
				continue
			}
			filtered = append(filtered, s)
		}
		policies[i].Subjects = filtered
	}
	if !changed {
		return nil
	}

	out, err := json.Marshal(policies)
	if err != nil {
		return fmt.Errorf("marshal TLS policies: %w", err)
	}
	req, err := http.NewRequest("PATCH", url, bytes.NewReader(out))
	if err != nil {
		return fmt.Errorf("build PATCH request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	patchResp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("patch TLS policies: %w", err)
	}
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(patchResp.Body)
		return fmt.Errorf("caddy returned %d patching TLS policies: %s", patchResp.StatusCode, string(errBody))
	}
	return nil
}

// ensureDNSIssuers gives a policy's ACME issuers the configured DNS-01
// challenge config, reporting whether it changed anything.
//
// Wildcard subjects (`*.example.com`) can ONLY be issued via DNS-01 —
// HTTP-01 and TLS-ALPN-01 categorically cannot solve them. A policy created
// before DNS-01 was configured keeps its original issuers, so appending a
// wildcard to it produces a subject Caddy never attempts: no certificate, no
// error, nothing in the log to notice (#1066).
//
// Repairs issuers in place — attaching `challenges.dns` to each ACME issuer
// that lacks it — rather than replacing the array. The previous wholesale
// `policy.Issuers = issuersFor(...)` silently discarded any per-issuer field
// issuersFor doesn't itself reproduce: external_account (EAB), Module was
// always overwritten to "acme", CA became Let's Encrypt for anything but
// (#1671). Only reachable via a hand-edited policy today (ProxyManager is
// the only in-tree writer), but the loss was permanent — persisted back to
// Caddy on the very next reconcile tick.
//
// A non-ACME issuer (Module != "acme", e.g. "internal") is left completely
// alone: it categorically cannot solve DNS-01, and attaching the challenge
// config anyway is meaningless for an internal CA and would misrepresent an
// issuer that was deliberately kept off public ACME.
//
// A no-op when DNS-01 isn't configured (nothing to add), when the policy has
// no issuers at all to repair, or when every ACME issuer already carries it
// — so this is safe to call on every provision.
func (p *ProxyManager) ensureDNSIssuers(policy *CaddyTLSAutomationPolicy) bool {
	if p.dnsChallenge == nil {
		return false
	}
	if len(policy.Issuers) == 0 {
		policy.Issuers = issuersFor(p.dnsChallenge)
		return true
	}
	if policyHasDNSChallenge(*policy) {
		return false
	}
	changed := false
	for i := range policy.Issuers {
		issuer := &policy.Issuers[i]
		if issuer.Module != "acme" {
			continue
		}
		if issuer.Challenges == nil || issuer.Challenges.DNS == nil {
			issuer.Challenges = p.dnsChallenge
			changed = true
		}
	}
	return changed
}

// ensureIssuerEmail gives a policy's issuers the configured ACME contact
// address, reporting whether it changed anything.
//
// Every host we run already has TLS policies, and both ProvisionTLS and
// EnsureTLSSubjects only ever append *subjects* — issuers on an existing
// policy are left exactly as they were written. So configuring
// CONTAINARIUM_ACME_EMAIL would fix nothing anywhere without a repair here:
// the address would only reach hosts that had never provisioned TLS before,
// which is none of them (#1616).
//
// A no-op when no email is configured — in particular it never blanks an
// address an operator set by hand through Caddy's admin API — and a no-op
// when every issuer already carries the right one, so the every-tick
// no-change path performs no write.
func (p *ProxyManager) ensureIssuerEmail(policy *CaddyTLSAutomationPolicy) bool {
	email := ACMEEmailFromEnv()

	// Nothing configured: there is no address to write, but there may still be
	// a ZeroSSL issuer that can never register sitting in the policy — which
	// is the state every host in the fleet is in today. Drop it, for the same
	// reason issuersFor no longer emits it: a fallback that cannot register is
	// worse than no fallback, because the policy reads as redundant while
	// every issuance pays a failed round trip for it.
	//
	// An issuer carrying its own address (set by hand through Caddy's admin
	// API) can register, so it is left alone — as is any address already
	// there.
	if email == "" {
		return p.dropUnregisterableZeroSSL(policy)
	}

	changed := false
	for i := range policy.Issuers {
		if policy.Issuers[i].Email != email {
			policy.Issuers[i].Email = email
			changed = true
		}
	}
	return changed
}

// dropUnregisterableZeroSSL removes ZeroSSL issuers that carry no contact
// address at all, reporting whether it removed any. Idempotent: once removed
// there is nothing left to match, so the reconcile tick goes back to writing
// nothing.
func (p *ProxyManager) dropUnregisterableZeroSSL(policy *CaddyTLSAutomationPolicy) bool {
	kept := make([]CaddyTLSIssuer, 0, len(policy.Issuers))
	dropped := false
	for _, issuer := range policy.Issuers {
		if issuer.CA == zeroSSLDirectory && issuer.Email == "" {
			dropped = true
			continue
		}
		kept = append(kept, issuer)
	}
	if !dropped {
		return false
	}
	// `issuers` is omitempty: an empty array vanishes from the emitted JSON
	// and Caddy falls back to its own defaults — which include the ZeroSSL
	// issuer we just removed. Substitute the issuers we would emit for a fresh
	// policy instead, so the field is always explicitly present.
	if len(kept) == 0 {
		kept = issuersFor(p.dnsChallenge)
	}
	policy.Issuers = kept
	return true
}

// policyHasDNSChallenge reports whether EVERY ACME issuer can solve DNS-01.
//
// Every, not any: Caddy tries issuers in order, and one that cannot solve the
// challenge is a failed attempt for the wildcard rather than a skipped one.
// A non-ACME issuer (Module != "acme") is exempt — it can never solve DNS-01
// and ensureDNSIssuers deliberately leaves it untouched (#1671), so its
// absence of `challenges` isn't a gap this function should report.
func policyHasDNSChallenge(policy CaddyTLSAutomationPolicy) bool {
	for _, issuer := range policy.Issuers {
		if issuer.Module != "acme" {
			continue
		}
		if issuer.Challenges == nil || issuer.Challenges.DNS == nil {
			return false
		}
	}
	return true
}

// patchTLSPolicies writes the policy array back to Caddy.
func (p *ProxyManager) patchTLSPolicies(url string, policies []CaddyTLSAutomationPolicy) error {
	policyJSON, err := json.Marshal(policies)
	if err != nil {
		return fmt.Errorf("failed to marshal policies: %w", err)
	}
