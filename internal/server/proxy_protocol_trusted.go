package server

import (
	"fmt"
	"net/netip"
	"strings"
)

// validateProxyProtocolTrusted refuses the daemon's --proxy-protocol
// flag setup when the trust list is empty or contains a wildcard.
// Audit finding C-MED-1.
//
// The L4 proxy layer (internal/app/l4_proxy.go) makes the same
// check lazily when Caddy is reconfigured. We replicate it at
// daemon startup so a misconfig fails visibly at boot instead of
// at first Caddy update — operators see the error in the boot
// log rather than in journalctl traces 30 minutes later.
func validateProxyProtocolTrusted(cidrs []string) error {
	if len(cidrs) == 0 {
