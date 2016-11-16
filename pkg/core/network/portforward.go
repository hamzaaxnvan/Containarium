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
// Scoped with `-m addrtype --dst-type LOCAL`: only packets addressed to one
// of this host's own addresses are redirected, whatever their source. That
// makes hairpin work in the kernel — a box on the bridge that opens the
// host's public name reaches Caddy by DNAT, same as an outside client — and
// stops the rule capturing traffic that is only being forwarded through the
// host (#2732). The return path for bridge clients is the POSTROUTING
// MASQUERADE rule (addMasqueradeRule): without it Caddy would answer a
// same-subnet box directly and the box would drop the reply.
//
// The pre-#2732 shape was `! -s <bridge-subnet>` with no destination match;
// legacyCaddyPreRoutingRules clears it on upgrade.
func caddyPreRoutingRule(port int, caddyIP string) []string {
	return []string{"PREROUTING",
		"-p", "tcp", "-m", "addrtype", "--dst-type", "LOCAL",
		"--dport", fmt.Sprintf("%d", port),
		"-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%d", caddyIP, port)}
}

// ensurePreRoutingRule adds a PREROUTING DNAT rule if it's not already present.
func (pf *PortForwarder) ensurePreRoutingRule(port int) error {
	if _, err := pf.ipt(append([]string{"-t", "nat", "-C"}, caddyPreRoutingRule(port, pf.caddyIP)...)...); err == nil {
		return nil
	}
	return pf.addPreRoutingRule(port)
}

// ensureOutputRule adds an OUTPUT DNAT rule if it's not already present.
func (pf *PortForwarder) ensureOutputRule(port int) error {
	if _, err := pf.ipt("-t", "nat", "-C", "OUTPUT",
		"-p", "tcp", "-d", "127.0.0.0/8", "--dport", fmt.Sprintf("%d", port),
		"-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%d", pf.caddyIP, port)); err == nil {
		return nil
	}
	return pf.addOutputRule(port)
}

// enableRouteLocalnet sets net.ipv4.conf.all.route_localnet=1, which is
// required to DNAT 127.0.0.0/8 traffic out a non-loopback interface.
// Persisted via /etc/sysctl.d/ so it survives reboots.
func (pf *PortForwarder) enableRouteLocalnet() error {
	cmd := exec.Command("sysctl", "-w", "net.ipv4.conf.all.route_localnet=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sysctl failed: %w, output: %s", err, string(output))
	}
	// Best-effort persistence so the setting survives reboots.
	const path = "/etc/sysctl.d/99-containarium-route-localnet.conf"
	const body = "# containarium: required for DNAT of 127.0.0.0/8 (tunneled-primary path)\nnet.ipv4.conf.all.route_localnet = 1\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec // G306: sysctl drop-in must be world-readable for the system to load it
		log.Printf("  Note: could not persist sysctl to %s: %v (the runtime value is set, but it'll reset on reboot)", path, err)
	}
	return nil
}

// addOutputRule mirrors addPreRoutingRule but on the OUTPUT chain, so
// locally-generated packets to 127.0.0.0/8:port get DNAT'd to the Caddy
// container. This is the path used by tunneled-primary tunnel clients
// (slice 6) which dial 127.0.0.1:port to forward inbound bytes.
func (pf *PortForwarder) addOutputRule(port int) error {
	output, err := pf.ipt("-t", "nat", "-A", "OUTPUT",
		"-p", "tcp", "-d", "127.0.0.0/8", "--dport", fmt.Sprintf("%d", port),
		"-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%d", pf.caddyIP, port))
	if err != nil {
		return fmt.Errorf("iptables OUTPUT failed: %w, output: %s", err, string(output))
	}
	return nil
}

// enableIPForwarding enables IP forwarding in the kernel
func (pf *PortForwarder) enableIPForwarding() error {
	cmd := exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sysctl failed: %w, output: %s", err, string(output))
	}
	return nil
}

// EnableConntrackAccounting enables conntrack byte/packet accounting
// This is required for traffic monitoring to get accurate byte counters
func EnableConntrackAccounting() error {
	// Enable conntrack accounting
	cmd := exec.Command("sysctl", "-w", "net.netfilter.nf_conntrack_acct=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to enable conntrack accounting: %w, output: %s", err, string(output))
	}
	log.Printf("Conntrack accounting enabled")
	return nil
}

// addPreRoutingRule adds a PREROUTING DNAT rule for the specified port.
// Containers still reach external HTTPS services (registries, Let's Encrypt)
// because the rule only matches traffic addressed to this host.
func (pf *PortForwarder) addPreRoutingRule(port int) error {
	output, err := pf.ipt(append([]string{"-t", "nat", "-A"}, caddyPreRoutingRule(port, pf.caddyIP)...)...)
	if err != nil {
		return fmt.Errorf("iptables failed: %w, output: %s", err, string(output))
	}
	return nil
}

// addMasqueradeRule adds a POSTROUTING MASQUERADE rule for return traffic
func (pf *PortForwarder) addMasqueradeRule() error {
	// Check if rule already exists
	if _, err := pf.ipt("-t", "nat", "-C", "POSTROUTING", "-d", pf.caddyIP, "-j", "MASQUERADE"); err == nil {
		return nil // Rule already exists
	}

	output, err := pf.ipt("-t", "nat", "-A", "POSTROUTING", "-d", pf.caddyIP, "-j", "MASQUERADE")
	if err != nil {
		return fmt.Errorf("iptables failed: %w, output: %s", err, string(output))
	}
	return nil
}

// RemovePortForwarding removes the port forwarding rules
func (pf *PortForwarder) RemovePortForwarding() error {
	log.Printf("Removing port forwarding rules for Caddy (%s)...", pf.caddyIP)

	// Remove PREROUTING rules
	pf.removePreRoutingRule(80)
	pf.removePreRoutingRule(443)

	// Remove OUTPUT rules
	pf.removeOutputRule(80)
	pf.removeOutputRule(443)

	// Remove MASQUERADE rule
	pf.removeMasqueradeRule()

	return nil
}

// removePreRoutingRule removes a PREROUTING DNAT rule
func (pf *PortForwarder) removePreRoutingRule(port int) {
	if _, err := pf.ipt(append([]string{"-t", "nat", "-D"}, caddyPreRoutingRule(port, pf.caddyIP)...)...); err != nil {
		log.Printf("  removePreRoutingRule: rule may not exist (ignored): %v", err)
	}
}

// removeOutputRule removes the OUTPUT-chain DNAT rule for tunneled-primary
// loopback traffic.
func (pf *PortForwarder) removeOutputRule(port int) {
	if _, err := pf.ipt("-t", "nat", "-D", "OUTPUT",
