package server

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/hostcheck"
	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// tenantApplied is what one reconcile pass actually loaded into the kernel for
// a tenant: the per-veth config written for its running boxes (after the
// host's enforce guard) and how many of those boxes it reached.
type tenantApplied struct {
	Mode           uint8                  // netbpf.ModeLogOnly | netbpf.ModeEnforce, as written to veth_policy
	EgressMode     uint8                  // as written to veth_policy
	BoxesLoaded    int                    // running boxes whose config was written and program attached
	BoxesNotLoaded int                    // running boxes with no loaded config (no veth, write or attach failed)
	HasDomains     bool                   // the loaded allow-list includes resolved egress_domains
	Source         pb.NetworkPolicySource // the host default or a stored policy; UNSPECIFIED when neither is known
	// EgressIncomplete: an add or removal in the tenant's egress allow-list
	// failed this pass, so the kernel list differs from the policy (#2983).
	EgressIncomplete bool
}

// appliedSnapshot is the enforcer's answer for one tenant.
type appliedSnapshot struct {
	Running    bool // the BPF program is loaded on this daemon
	Reconciled bool // at least one reconcile pass has applied the maps
	Found      bool // the last pass saw a running box of this tenant
	Tenant     tenantApplied
	// ReconciledAt is when the last completed pass ended; Interval is the
	// enforcer's reconcile interval, which decides when that pass is stale.
	ReconciledAt time.Time
	Interval     time.Duration
}

// staleAfterPasses is how many reconcile intervals the last completed pass may
// age before the status stops trusting it.
const staleAfterPasses = 3

// appliedStateSource is the slice of the enforcer GetNetworkPolicyStatus reads.
// *NetworkPolicyEnforcer satisfies it.
type appliedStateSource interface {
	AppliedState(tenant string) appliedSnapshot
}

// appliedFromPass builds the per-tenant applied state from one reconcile pass:
// the plan it applied and, per planned veth, whether the config write and the
// attach both succeeded. A running box without a resolved veth never reaches
// the plan's veth map, so it is counted as not loaded from the views.
//
// egressIncomplete marks a tenant id whose allow-list write failed this pass,
// and egressNames names it. The mark is keyed by the failed entry's tenant, not
// by the running views (#3051): a tenant with only stopped boxes, or with no box
// left after its policy was removed, still gets a record carrying it.
func appliedFromPass(views []containerView, plan reconcilePlan, loaded map[int]bool, policies map[string]netpolicy.CompiledPolicy, egressIncomplete map[uint32]bool, egressNames map[uint32]string) map[string]tenantApplied {
	out := make(map[string]tenantApplied)
	for _, v := range views {
		if !v.Running || v.ValidateOnly {
			continue // a validate-only instance is not a tenant's box (#2618)
		}
		a := out[v.Tenant]
		cfg, planned := plan.vethPolicy[v.Ifindex]
		if v.HasVeth && planned && loaded[v.Ifindex] {
			a.Mode, a.EgressMode = cfg.Mode, cfg.EgressMode
			a.BoxesLoaded++
			a.HasDomains = len(policies[v.Tenant].EgressDomains) > 0
			a.Source = policySource(policies, v.Tenant)
		} else {
			a.BoxesNotLoaded++
		}
		out[v.Tenant] = a
	}
	for id, bad := range egressIncomplete {
		name, ok := egressNames[id]
		if !bad || !ok {
			continue
		}
		a := out[name]
		a.EgressIncomplete = true
		out[name] = a
	}
	return out
}

// policySource says where a tenant's loaded policy came from. A tenant with no
// entry (default posture off, nothing stored) is unknown, not "default".
func policySource(policies map[string]netpolicy.CompiledPolicy, tenant string) pb.NetworkPolicySource {
	c, ok := policies[tenant]
	switch {
	case !ok:
		return pb.NetworkPolicySource_NETWORK_POLICY_SOURCE_UNSPECIFIED
	case c.IsHostDefault:
		return pb.NetworkPolicySource_NETWORK_POLICY_SOURCE_HOST_DEFAULT
	default:
		return pb.NetworkPolicySource_NETWORK_POLICY_SOURCE_STORED
	}
}

// SetEnforcementArmed records whether this host arms enforcement
// (CONTAINARIUM_NETWORK_POLICY_ENFORCE). Startup-only.
func (s *NetworkPolicyServer) SetEnforcementArmed(armed bool) { s.enforceArmed = armed }

// SetAppliedStateSource wires the enforcer whose loaded state
// GetNetworkPolicyStatus reports. Startup-only; nil (the default) reports the
// enforcer as not running.
func (s *NetworkPolicyServer) SetAppliedStateSource(src appliedStateSource) { s.applied = src }

