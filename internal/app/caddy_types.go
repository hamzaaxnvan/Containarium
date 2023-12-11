package app

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Caddy API Types
// These types provide type-safe representations of Caddy's JSON API structures.
// Reference: https://caddyserver.com/docs/json/

// CaddyServerConfig represents a Caddy HTTP server configuration
type CaddyServerConfig struct {
	Listen           []string               `json:"listen"`
	ListenerWrappers []CaddyListenerWrapper `json:"listener_wrappers,omitempty"`
	Routes           []CaddyRouteTyped      `json:"routes"` // No omitempty - Caddy needs empty array to exist
	TrustedProxies   *CaddyTrustedProxies   `json:"trusted_proxies,omitempty"`
	// ClientIPHeaders names the request headers Caddy derives the real client
	// IP from when the peer is inside TrustedProxies — e.g. Cf-Connecting-Ip
	// for a Cloudflare-fronted host. Empty keeps Caddy's default (peer IP,
	// or the PROXY-decoded source). See #1829.
	ClientIPHeaders []string `json:"client_ip_headers,omitempty"`
}

// CaddyListenerWrapper represents one entry in a server's listener_wrappers
// chain. The order matters: proxy_protocol must come before tls so the PROXY
// header is consumed before the TLS parser runs.
type CaddyListenerWrapper struct {
	Wrapper string   `json:"wrapper"`           // e.g. "proxy_protocol", "tls"
	Timeout string   `json:"timeout,omitempty"` // e.g. "5s" — only meaningful for proxy_protocol
	Allow   []string `json:"allow,omitempty"`   // CIDR list of trusted senders for proxy_protocol
}

// CaddyTrustedProxies marks IP ranges whose forwarded IP information Caddy will
// trust. Combined with the proxy_protocol listener wrapper, the parsed source
// address propagates into reverse_proxy as X-Forwarded-For for upstream
// containers.
type CaddyTrustedProxies struct {
	Source string   `json:"source"` // "static"
	Ranges []string `json:"ranges"`
}

// CaddyRouteTyped represents a fully typed Caddy route configuration
type CaddyRouteTyped struct {
	ID     string            `json:"@id,omitempty"`
	Match  []CaddyMatchTyped `json:"match,omitempty"`
	Handle []CaddyHandler    `json:"handle,omitempty"`
}

// CaddyMatchTyped represents typed match conditions for a route
type CaddyMatchTyped struct {
	Host []string `json:"host,omitempty"`
	Path []string `json:"path,omitempty"`
}

// CaddyHandler is an interface for Caddy handlers
// This allows type-safe handling of different handler types
type CaddyHandler interface {
	HandlerName() string
}

// CaddyReverseProxyHandler represents a reverse_proxy handler configuration
type CaddyReverseProxyHandler struct {
	Handler   string               `json:"handler"` // Always "reverse_proxy"
	Upstreams []CaddyUpstreamTyped `json:"upstreams"`
	Headers   *CaddyHeadersConfig  `json:"headers,omitempty"`
	Transport *CaddyHTTPTransport  `json:"transport,omitempty"` // For gRPC/HTTP2 support
}

// CaddyHTTPTransport represents the HTTP transport configuration
// Used to enable HTTP/2 (h2c) for gRPC proxying
type CaddyHTTPTransport struct {
	Protocol string   `json:"protocol"`           // Always "http" for HTTP transport
	Versions []string `json:"versions,omitempty"` // ["h2c", "2"] for gRPC
}

// NewGRPCTransport creates a transport configuration for gRPC (HTTP/2 cleartext)
func NewGRPCTransport() *CaddyHTTPTransport {
	return &CaddyHTTPTransport{
		Protocol: "http",
		Versions: []string{"h2c", "2"},
	}
}

// HandlerName implements CaddyHandler
func (h CaddyReverseProxyHandler) HandlerName() string {
	return "reverse_proxy"
}

// CaddyUpstreamTyped represents a typed upstream server configuration
type CaddyUpstreamTyped struct {
	Dial string `json:"dial"` // Format: "host:port"
}

// CaddyHeadersConfig represents header manipulation configuration
type CaddyHeadersConfig struct {
	Request  *CaddyHeaderOps `json:"request,omitempty"`
	Response *CaddyHeaderOps `json:"response,omitempty"`
}

// CaddyHeaderOps represents header operations (add, set, delete)
type CaddyHeaderOps struct {
	Add    map[string][]string `json:"add,omitempty"`
	Set    map[string][]string `json:"set,omitempty"`
	Delete []string            `json:"delete,omitempty"`
}

// CaddyWAFHandler represents the coraza-caddy WAF handler (Tier 3 PR-3, #662).
// It runs BEFORE reverse_proxy in a route's Handle chain: Coraza parses the
// (already TLS-terminated, reassembled) HTTP request, evaluates the OWASP CRS +
// any virtual-patch rules carried in Directives, and either blocks (returns the
// configured deny status) or passes through to the upstream. The Caddy binary
// must be built with `--with github.com/corazawaf/coraza-caddy/v2` (see
// internal/hosting/caddy.go); JSON module id is `waf` under http.handlers.
type CaddyWAFHandler struct {
	Handler string `json:"handler"` // Always "waf"
	// Directives is the SecLang config the engine loads — the recommended Coraza
	// conf + CRS setup + the rule set, plus SecRuleEngine On/DetectionOnly.
	Directives string `json:"directives,omitempty"`
}

// HandlerName implements CaddyHandler
func (h CaddyWAFHandler) HandlerName() string {
	return "waf"
}

// DefaultWAFDirectives returns the SecLang the WAF handler loads: the
// recommended Coraza baseline, the CRS setup, and the full OWASP CRS, all from
// the rule files coraza-caddy embeds (the @-prefixed paths). enforce selects
// SecRuleEngine On (block) vs DetectionOnly (observe + log only) — mirroring the
// rest of the stack's observe-then-arm gating.
func DefaultWAFDirectives(enforce bool) string {
	engine := "DetectionOnly"
	if enforce {
		engine = "On"
	}
	return "Include @coraza.conf-recommended\n" +
		"Include @crs-setup.conf.example\n" +
		"Include @owasp_crs/*.conf\n" +
		"SecRuleEngine " + engine
}

// PrependWAF returns handlers with a CaddyWAFHandler inserted at the front (so it
// inspects before reverse_proxy), when enabled. When disabled it returns the
// handlers unchanged — so ingress routes are byte-identical to today unless an
// operator opts into the WAF. directives is the SecLang to load (see
// DefaultWAFDirectives).
func PrependWAF(handlers []CaddyHandler, enabled bool, directives string) []CaddyHandler {
	if !enabled {
		return handlers
	}
	out := make([]CaddyHandler, 0, len(handlers)+1)
	out = append(out, CaddyWAFHandler{Handler: "waf", Directives: directives})
	return append(out, handlers...)
}

// CaddyStaticResponseHandler represents a static_response handler
type CaddyStaticResponseHandler struct {
	Handler    string              `json:"handler"` // Always "static_response"
	StatusCode int                 `json:"status_code,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       string              `json:"body,omitempty"`
}

// HandlerName implements CaddyHandler
func (h CaddyStaticResponseHandler) HandlerName() string {
	return "static_response"
}

// CaddyTLSAutomationPolicy represents a TLS automation policy
type CaddyTLSAutomationPolicy struct {
	Subjects      []string         `json:"subjects,omitempty"`
	Issuers       []CaddyTLSIssuer `json:"issuers,omitempty"`
	OnDemand      bool             `json:"on_demand,omitempty"`
	MustStaple    bool             `json:"must_staple,omitempty"`
	RenewalWindow string           `json:"renewal_window,omitempty"` // Duration string, e.g., "720h"
}
