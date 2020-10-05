// Package tenantguard computes and reconciles the network ACLs that keep one
// tenant's containers off another tenant's containers on a shared Incus
// bridge.
//
// Design: docs/architecture/tenant-network-guard.md. This file is the pure
// half: Compute renders one incus.ACLConfig per tenant from the live
// addresses on a host — allow the host gateway, the core initiators and the
// tenant's own boxes; everything else is dropped by the NIC's default
// ingress action. No I/O lives here; the reconciler that reads the host
// and writes the ACLs is a separate file so the policy can be pinned by
// tests without a fake backend.
package tenantguard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"sort"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// Box is one tenant container as the reconciler sees it.
type Box struct {
	Name   string
	Tenant string
	// IPv4 is the box's bridge address; invalid when it has none yet.
	IPv4 netip.Addr
	// IPv6 lists any global/ULA IPv6 addresses on the bridge NIC. Empty on
	// daemons that only report IPv4 (today); the ACL still default-drops
	// IPv6, so an unlisted sibling is denied, never a stranger admitted.
	IPv6 []netip.Addr
}

// Inputs is everything Compute needs about a host.
type Inputs struct {
	// BridgeCIDR is the tenant bridge subnet (e.g. 10.100.0.0/24).
	BridgeCIDR netip.Prefix
	// HostGateway is the bridge address of the host itself: DHCP, DNS, the
	// daemon, the SSH sentinel path and passthrough routes all come from it.
	HostGateway netip.Addr
	// Initiators are the core-role containers that open connections to
	// tenant boxes (Caddy reverse-proxying an exposed port, a co-located
	// control plane actuating a box). Keyed by role; a role with no address
	// yet is absent and rendered on the next pass.
	Initiators map[incus.Role][]netip.Addr
	// Boxes are the tenant containers on the host. A box whose tenant could
	// not be resolved must not be here; the reconciler reports it instead.
	Boxes []Box
	// CrossTenantAllow maps a tenant to the tenants whose boxes may reach
	// it (NetworkPolicy.allow_from_tenants, #2359). One-directional: an
	// entry alice → [bob] admits bob's boxes on alice's NICs only. A listed
	// tenant with no box on this host contributes nothing.
	CrossTenantAllow map[string][]string
}

