package server

import (
	"fmt"
	"net/netip"
	"strings"
)

// validateTrustedProxyCIDRs validates the daemon's --trusted-proxy-cidrs
// flag (#1829): the extra ranges appended to Caddy's trusted_proxies so a
// CDN-supplied client-IP header is honored only from the CDN's own networks.
//
// Unlike --proxy-protocol-trusted, an EMPTY list is valid — the feature is
// opt-in and off by default. Wildcards and malformed entries are refused at
// startup so a misconfig fails visibly at boot, matching
// validateProxyProtocolTrusted.
func validateTrustedProxyCIDRs(cidrs []string) error {
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			return fmt.Errorf("--trusted-proxy-cidrs contains an empty entry")
