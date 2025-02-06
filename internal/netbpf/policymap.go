package netbpf

import (
	"fmt"

	"github.com/footprintai/containarium/internal/netpolicy"
	"github.com/footprintai/containarium/internal/safecast"
)

// BPF mode constants — mirror the #defines in
// experimental/ebpf-phaseA/netpolicy.bpf.c, which in turn mirror
// pb.NetworkPolicyMode. Phase A only ever loads ModeLogOnly.
const (
	ModeLogOnly uint8 = 1
	ModeEnforce uint8 = 2
)

// PolicyConfig is the per-veth value the loader writes into the BPF veth_policy
// map (keyed by the container's host veth ifindex). Field layout mirrors
// `struct policy_cfg` in netpolicy.bpf.c.
type PolicyConfig struct {
	TenantID      uint32
	Mode          uint8
	AllowIntra    uint8
	AllowMetadata uint8
	// EgressMode is the mode of the egress allow-list decision alone
	// (ModeLogOnly | ModeEnforce); Mode governs every other check. It occupies
	// the struct's former pad byte, so the value stays 8 bytes. The program
	// treats 0 as "follow Mode"; CompileConfig always writes it explicitly.
	EgressMode uint8
	// BoxIP is the box's own IPv4 in network byte order (#2618), appended to
	// the struct as `box_ip`. The program drops (enforce) or logs (log_only) a
	// packet the box sends from any other address except 0.0.0.0 (DHCP), and
	// seeds an inbound flow from a frame that entered the host through this
	// veth only when the frame's source is this address. The zero value means
	// unknown: no source validation on the veth and no seeding from it. The
	// daemon sets it from the container view (planReconcile), not from the
	// policy, so CompileConfig leaves it zero. It must come from a
	// host-authoritative source (the NIC's configured address or the bridge's
	// DHCP lease), never from what the guest reports about itself.
	BoxIP [4]byte
	// Flags is the `flags` byte appended after box_ip (PolicyFlag*).
	Flags PolicyFlags
}

// PolicyFlags mirrors the POLICY_FLAG_* bits of `struct policy_cfg`.
type PolicyFlags uint8

const (
	// PolicyFlagValidateOnly marks a veth the daemon does not police (the
	// control plane, an instance with no tenant): its sender hook only stamps
	// frames with the source-validation mark and passes — no accounting, no
	// policy, no events, never a drop. Every Incus instance on the bridge gets
	// at least this entry so that a frame without the mark can only be the
	// host's own or routed in from outside (#2618).
	PolicyFlagValidateOnly PolicyFlags = 1
)

// ValidateOnlyConfig is the veth_policy entry for an instance the daemon does
// not police: source-validation marking only, with the host-assigned address.
func ValidateOnlyConfig(boxIP [4]byte) PolicyConfig {
	return PolicyConfig{BoxIP: boxIP, Flags: PolicyFlagValidateOnly}
}

// EgressEntry is one allowed-egress LPM-trie entry the loader writes into the
// BPF egress_cidr map. It is the tenant-scoped destination prefix the sender's
// policy permits. Field layout mirrors `struct egress_key` in netpolicy.bpf.c
// (prefixlen counts the 32-bit exact tenant match plus the IPv4 prefix bits;
// Addr holds the masked network address in network byte order).
type EgressEntry struct {
	PrefixLen uint32
	TenantID  uint32
	Addr      [4]byte
}

// tenantPrefixBits is the LPM prefix length contributed by the tenant_id field:
// a tenant match is always exact (all 32 bits), then the IPv4 prefix bits
// follow.
const tenantPrefixBits = 32

// DenyEntry is one virtual-patch deny rule (#660) the loader writes into the BPF
// deny_cidr LPM-trie map. The key (PrefixLen, TenantID, Addr) mirrors EgressEntry
// — a tenant-scoped destination prefix — and the value (Port, Proto) further
// scopes the block to a single service (0 = any). Deny beats the egress
// allow-list. The struct is comparable so reconcile can diff desired vs.
// installed; DenyKey is the kernel map key (CIDR only — the value carries
// port/proto), so changing only a rule's port updates the same map entry rather
// than churning two.
