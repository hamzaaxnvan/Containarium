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
			// marking-only entry so the receiving side can tell its frames
			// from the host's (#2618); the address is the host-known one or 0.
			if v.Running && v.HasVeth {
				var ip [4]byte
				if v.HasHostIP {
					ip = v.HostIP
				}
				plan.vethPolicy[v.Ifindex] = netbpf.ValidateOnlyConfig(ip)
				plan.ifName[v.Ifindex] = v.Name
			}
			continue
		}
		if v.HasIP {
			plan.ipTenant[v.IP] = v.TenantID
		}
		policy, hasPolicy := policies[v.Tenant]

		if v.Running && v.HasVeth {
			var cfg netbpf.PolicyConfig
			if hasPolicy {
				cfg = netbpf.CompileConfig(v.TenantID, policy)
			} else {
				cfg = netbpf.PolicyConfig{TenantID: v.TenantID, Mode: netbpf.ModeLogOnly, EgressMode: netbpf.ModeLogOnly}
			}
			// Safety guard: enforcement only drops when armed daemon-wide. It
			// applies to both modes — the egress allow-list's own mode cannot
			// arm drops the guard has not armed.
			if !enforceEnabled {
				if cfg.Mode == netbpf.ModeEnforce {
					cfg.Mode = netbpf.ModeLogOnly
				}
				if cfg.EgressMode == netbpf.ModeEnforce {
					cfg.EgressMode = netbpf.ModeLogOnly
				}
			}
			// The box's own address (#2618): the program validates the box's
			// source against it and seeds inbound flows only from frames whose
			// source is the sending veth's address. HOST-known only (device
			// config or DHCP lease) — never v.IP, which the guest reports and
			// root in the box can change. Unknown stays zero, which the program
			// reads as "cannot validate this veth".
			if v.HasHostIP {
				cfg.BoxIP = v.HostIP
			}
			plan.vethPolicy[v.Ifindex] = cfg
			plan.ifName[v.Ifindex] = v.Name
		}

		if hasPolicy && !egressDone[v.TenantID] {
			if entries, err := netbpf.CompileEgress(v.TenantID, policy); err == nil {
				plan.egress = append(plan.egress, entries...)
			}
			// Virtual-patch deny entries (#660), once per tenant. Expired rules are
			// already filtered out of policy.DenyRules by the daemon (compiledPolicies)
			// before planning, so anything here is active.
			if entries, err := netbpf.CompileDeny(v.TenantID, policy); err == nil {
				plan.deny = append(plan.deny, entries...)
			}
			egressDone[v.TenantID] = true
		}
	}
	return plan
}

// withDefaultPolicies returns policies plus, for every tenant seen in views
// that has no stored policy, the default posture from build (#2577 stage 1). A
// stored policy always wins; a nil build (default posture off) or a tenant the
// builder declines leaves the tenant on the bare log-only fallback in
// planReconcile. The input map is not modified.
func withDefaultPolicies(policies map[string]netpolicy.CompiledPolicy, views []containerView, build func(tenant string) (netpolicy.CompiledPolicy, bool)) map[string]netpolicy.CompiledPolicy {
	if build == nil {
		return policies
	}
	out := make(map[string]netpolicy.CompiledPolicy, len(policies))
	for k, v := range policies {
