package server

import (
	"log"
	"strconv"
	"time"

	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/netpolicy"
)

// containerView is a reconcile-time snapshot of one managed container: its
// tenant, assigned tenant ID, IPv4 (if resolved), and host veth ifindex (if
// running + resolved). gather() builds these (Linux veth resolution);
// planReconcile() turns them + the compiled policies into BPF map entries.
type containerView struct {
	Name     string
	Tenant   string
	TenantID uint32
	// IP is the address the instance state reports — what the GUEST configured
	// on itself. It feeds ip_tenant (which tenant a destination belongs to) and
	// nothing the box's own packets are validated against.
	IP    [4]byte
	HasIP bool
	// HostIP is the box's address as the HOST knows it — the NIC device's
	// static ipv4.address or the bridge's DHCP lease — the only source the
	// program's source validation and inbound-flow seeding trust (#2618). Root
	// in the box cannot change it.
	HostIP    [4]byte
	HasHostIP bool
	Ifindex   int
	HasVeth   bool
	Running   bool
	// ValidateOnly marks an instance the daemon does not police (the control
	// plane, an instance with no tenant): it gets a veth entry that only stamps
	// its frames with the source-validation mark (#2618), no policy, no
	// ip_tenant row, no per-tenant status.
	ValidateOnly bool
}

// reconcilePlan is the desired BPF map state for one reconcile pass — pure data
// so planReconcile is unit-testable without a kernel.
type reconcilePlan struct {
	ipTenant   map[[4]byte]uint32          // container IP -> tenant id
	vethPolicy map[int]netbpf.PolicyConfig // running container veth ifindex -> policy config
	ifName     map[int]string              // ifindex -> container name (for bookkeeping)
	egress     []netbpf.EgressEntry        // per-tenant egress allow-list entries
	deny       []netbpf.DenyEntry          // per-tenant virtual-patch deny entries (#660)
}

// planReconcile computes the desired BPF map state from the current container
// views and the compiled per-tenant policies. A container with no stored policy
// gets the Phase A default (log-only, no intra-tenant, empty egress) so its
// outbound is observed rather than silently unmanaged.
//
// enforceEnabled is the daemon-wide safety guard (Phase B): when false, a
// policy's ENFORCE mode is downgraded to LOG_ONLY before it reaches the kernel,
// so a stored `--mode enforce` policy is still observation-only until the
// operator explicitly arms enforcement. This prevents a misconfigured policy
// (e.g. one that forgot to allow DNS) from blackholing a container the moment
// it's set; operators soak in log_only, watch the would-deny logs, complete the
// allow-list, then arm enforce.
func planReconcile(views []containerView, policies map[string]netpolicy.CompiledPolicy, enforceEnabled bool) reconcilePlan {
	plan := reconcilePlan{
		ipTenant:   make(map[[4]byte]uint32),
		vethPolicy: make(map[int]netbpf.PolicyConfig),
		ifName:     make(map[int]string),
	}
	// egress entries are per tenant, not per container — emit each tenant's set
	// once, keyed by the tenant IDs we actually saw.
	egressDone := make(map[uint32]bool)

	for _, v := range views {
		if v.ValidateOnly {
			// Not a tenant: no ip_tenant row, no policy. Its veth gets the
