package network

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
)

// PortForwarder manages iptables port forwarding rules for Caddy
type PortForwarder struct {
	caddyIP     string
	networkCIDR string // Container network CIDR (e.g., "10.0.3.0/24"); the 80/443 DNAT no longer excludes it (#2732)

	// ipt runs one iptables invocation. Tests substitute it to record the
	// order of operations or to target a throwaway network namespace.
	ipt func(args ...string) ([]byte, error)
}

// execIPTables runs iptables on this host.
func execIPTables(args ...string) ([]byte, error) {
	// #nosec G204 -- fixed binary, no shell. Arguments are this package's own
	// rule specs (constant ports, plus a Caddy IP read from Incus or passed
	// by a root operator via --caddy-ip) or rule specs read back from this
	// host's own `iptables -t nat -S` output.
	return exec.Command("iptables", args...).CombinedOutput()
}

// NewPortForwarder creates a new port forwarder for the given Caddy IP.
// The container network CIDR is derived from the Caddy IP (assumes /24).
func NewPortForwarder(caddyIP string) *PortForwarder {
	return NewPortForwarderWithNetwork(caddyIP, "")
}

// NewPortForwarderWithNetwork creates a new port forwarder with explicit network CIDR
func NewPortForwarderWithNetwork(caddyIP, networkCIDR string) *PortForwarder {
	// If no network CIDR provided, derive from Caddy IP (assume /24)
	if networkCIDR == "" {
		networkCIDR = deriveNetworkCIDR(caddyIP)
	}
	return &PortForwarder{
		caddyIP:     caddyIP,
		networkCIDR: networkCIDR,
		ipt:         execIPTables,
	}
}

// deriveNetworkCIDR derives a /24 network CIDR from an IP address
// e.g., "10.0.3.111" -> "10.0.3.0/24"
func deriveNetworkCIDR(ip string) string {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return ip // Return as-is if not a valid IP
	}
	return fmt.Sprintf("%s.%s.%s.0/24", parts[0], parts[1], parts[2])
}

// SetupPortForwarding configures iptables to forward ports 80 and 443 to Caddy.
// Required for Let's Encrypt certificate provisioning and HTTPS traffic.
//
// Three iptables paths are needed:
//
//  1. PREROUTING DNAT — for traffic arriving on a non-loopback interface
//     (the normal external case via the GLB).
//  2. OUTPUT DNAT — for locally-generated traffic to 127.0.0.0/8:443.
//     This is the path taken by tunnel-promoted primaries (slice 6/8):
//     the tunnel client receives a yamux stream and dials 127.0.0.1:port
//     to forward bytes locally. PREROUTING does not match local-origin
//     packets, so OUTPUT is needed.
//  3. POSTROUTING MASQUERADE — for return traffic.
//
// Plus one sysctl: net.ipv4.conf.all.route_localnet=1. By default the
// kernel refuses to route 127.0.0.0/8 packets out a non-loopback
// interface even after DNAT, so the tunneled-primary path silently
// drops without this. Setting it system-wide is safe — the Linux man
// page calls out that this is the right knob for "DNAT 127/8 to a
// non-local address" use case.
func (pf *PortForwarder) SetupPortForwarding() error {
	log.Printf("Setting up port forwarding to Caddy (%s)...", pf.caddyIP)

	// Enable IP forwarding
	if err := pf.enableIPForwarding(); err != nil {
		return fmt.Errorf("failed to enable IP forwarding: %w", err)
	}

	// Allow DNAT of 127.0.0.0/8 → caddyIP (required for tunneled
	// primaries; harmless otherwise).
	if err := pf.enableRouteLocalnet(); err != nil {
		log.Printf("  Warning: failed to set route_localnet=1: %v (tunneled-primary loopback path may not work)", err)
	}

	if err := pf.applyRules(); err != nil {
		return err
	}

	log.Printf("  Port forwarding configured: 80,443 -> %s (PREROUTING + OUTPUT)", pf.caddyIP)
	return nil
}

// applyRules installs the Caddy NAT rules and clears outdated ones, in an
// order that never leaves 80/443 without a DNAT to the current Caddy:
//
//  1. delete rules pointing at an old Caddy IP (they would shadow the new ones),
//  2. ensure the current rules,
//  3. only then delete the pre-#2732 `! -s <bridge>` shape. It targets the
//     same Caddy, so while both exist inbound traffic still lands there; if
//     step 2 fails, the old shape is left in place and keeps serving.
func (pf *PortForwarder) applyRules() error {
	// Clear Caddy DNAT/MASQUERADE rules that point at a *different* IP than the
	// current one. When core-caddy is recreated it comes back with a new
	// container IP; the per-rule existence checks below would then ADD rules for
	// the new IP while leaving the old ones in place. The stale rule sorts first
	// in the chain and matches, so all :443 traffic is DNAT'd to a container
	// that no longer exists and the TLS handshake returns nothing (#400).
	pf.reconcileStaleRules()

	// Each rule is added independently with its own existence check.
	// An older deploy may have PREROUTING but not OUTPUT — we want to
	// add the missing one without duplicating what's already there.
	if err := pf.ensurePreRoutingRule(80); err != nil {
		return fmt.Errorf("failed to add port 80 PREROUTING: %w", err)
	}
	if err := pf.ensurePreRoutingRule(443); err != nil {
		return fmt.Errorf("failed to add port 443 PREROUTING: %w", err)
	}
	if err := pf.ensureOutputRule(80); err != nil {
		return fmt.Errorf("failed to add port 80 OUTPUT: %w", err)
	}
	if err := pf.ensureOutputRule(443); err != nil {
		return fmt.Errorf("failed to add port 443 OUTPUT: %w", err)
	}
	if err := pf.addMasqueradeRule(); err != nil {
		return fmt.Errorf("failed to add masquerade rule: %w", err)
	}

	pf.clearLegacyRules()
	return nil
}

// caddyPreRoutingRule is the PREROUTING DNAT rule spec (chain onward, ready
// to follow -A/-C/-D) that sends port 80/443 traffic to Caddy.
//
