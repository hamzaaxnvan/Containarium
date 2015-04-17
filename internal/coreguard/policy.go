// Package coreguard computes the network ACLs that isolate the platform's
// core-role containers (core-postgres, core-victoriametrics, ...) from
// tenant containers sharing the same Incus bridge.
//
// Design: docs/architecture/core-infra-network-guard.md. This file is the
// pure half — a typed table of "who may legitimately reach which role on
// which port" and Compute, which renders it into one incus.ACLConfig per
// guarded role from the live addresses on a host. No I/O lives here; the
// reconciler that reads the host and writes the ACLs is a separate file so
// the table can be pinned by tests without a fake backend.
package coreguard

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// Inputs is everything Compute needs about a host: the tenant bridge, the
// daemon's address on it, where each core-role container currently is, and
// any co-located control planes. Addresses are live — the reconciler feeds
// whatever incus reports, so a core container that moves gets its rules
// re-rendered rather than stranded.
type Inputs struct {
	// BridgeCIDR is the tenant bridge subnet (e.g. 10.100.0.0/24). It is the
	// only non-/32 source the table ever uses, and only for the two ports
	// tenants are meant to reach.
	BridgeCIDR netip.Prefix
	// HostGateway is the bridge address of the host itself — where the
	// daemon's own connections (Postgres pool, Caddy admin, metrics) come
	// from.
	HostGateway netip.Addr
	// Core maps each core role present on the host to its IPv4. A role with
	// no address (still booting) is simply absent; its rules are omitted
	// from peers for this pass and rendered on the next.
	Core map[incus.Role]netip.Addr
	// ControlPlane lists any co-located control-plane containers
	// (incus.RoleControlPlane). They are sources in the table, never
	// guarded subjects.
	ControlPlane []netip.Addr
}

// Policy is Compute's output: one ACL per guarded role, plus the roles the
// table does not know. An unknown role still gets an (empty) ACL — attached,
// it default-drops everything, which is the safe failure — and is reported
// so the table gets extended deliberately instead of silently.
type Policy struct {
	ACLs         map[incus.Role]incus.ACLConfig
	UnknownRoles []incus.Role
}

// ACLName is the Incus network ACL name for a role's guard:
// containarium-core-guard-<role without the core- prefix>.
func ACLName(role incus.Role) string {
	return "containarium-core-guard-" + strings.TrimPrefix(string(role), "core-")
}

// sourceKind says who a listener rule admits. Every rule's source is one of
// these — there is deliberately no "anyone" kind.
type sourceKind uint8
