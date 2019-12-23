// Package egressproxy implements the host-side relay for "egress via client"
// (#808, Phase 2a).
//
// A box runs in its own network namespace and can reach the host's bridge
// gateway, but NOT a host-side `ssh -R` listener (host loopback) or, directly,
// an off-box network like the operator's tailnet. The relay bridges the two:
// it binds a bridge-gateway address the box can reach, accepts ONLY the target
// box's source IP (the multi-tenant boundary), and forwards each connection to
// a host-reachable upstream — typically the operator's SOCKS proxy reachable
// from the host (e.g. over Tailscale). The box's apps point at the relay as
// their SOCKS proxy and egress with the operator's IP.
//
// The relay is intentionally protocol-agnostic raw TCP: a SOCKS handshake from
// the box passes straight through to the upstream SOCKS server.
package egressproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Relay is a source-restricted TCP forwarder. The zero value is not usable;
// construct with New.
type Relay struct {
	listen   string
	upstream string
	allow    []netip.Prefix
	logf     func(string, ...any)

	mu sync.Mutex
	ln net.Listener
}

// New builds a relay. listen and upstream are host:port. allow is a list of
// source prefixes (CIDR) or bare IPs (treated as /32 or /128) permitted to use
// the relay; an empty allow list denies everyone (fail-closed). logf may be nil.
func New(listen, upstream string, allow []string, logf func(string, ...any)) (*Relay, error) {
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return nil, fmt.Errorf("invalid --listen %q: %w", listen, err)
	}
	if _, _, err := net.SplitHostPort(upstream); err != nil {
		return nil, fmt.Errorf("invalid --upstream %q: %w", upstream, err)
	}
	prefixes, err := parseAllow(allow)
	if err != nil {
		return nil, err
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Relay{listen: listen, upstream: upstream, allow: prefixes, logf: logf}, nil
}

// parseAllow turns CIDR/bare-IP strings into prefixes. A bare IP becomes a
// host route (/32 or /128).
func parseAllow(allow []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, a := range allow {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if strings.Contains(a, "/") {
			p, err := netip.ParsePrefix(a)
			if err != nil {
				return nil, fmt.Errorf("invalid --allow %q: %w", a, err)
			}
			out = append(out, p)
			continue
		}
		ip, err := netip.ParseAddr(a)
		if err != nil {
			return nil, fmt.Errorf("invalid --allow %q: %w", a, err)
