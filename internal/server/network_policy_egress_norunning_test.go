package server

import (
	"slices"
	"testing"

	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #3051: a failed allow-list write must reach the owning tenant's status even
// when that tenant has no running box. These drive the enforcer's egress step
// with a fake loader (fakeEgressApplier), record the pass the way reconcile
// does, and read the tenant's status through the RPC.

const egressIncompletePath = pb.NetworkPolicyUncoveredPath_NETWORK_POLICY_UNCOVERED_PATH_EGRESS_ALLOWLIST_INCOMPLETE

// egressPass runs one reconcile pass's plan, egress apply and record on e,
// with the egress writes going to f and every planned veth loaded.
func egressPass(t *testing.T, e *NetworkPolicyEnforcer, views []containerView, policies map[string]netpolicy.CompiledPolicy, f *fakeEgressApplier) {
	t.Helper()
	idName := make(map[uint32]string)
	for _, v := range views {
		idName[v.TenantID] = v.Tenant
	}
	plan := planReconcile(views, policies, true)
	loaded := make(map[int]bool, len(plan.vethPolicy))
	for ifindex := range plan.vethPolicy {
		loaded[ifindex] = true
	}
	incomplete, names := e.applyEgressPass(plan, idName, f)
	e.recordApplied(appliedFromPass(views, plan, loaded, policies, incomplete, names), statusNow)
}

// egressTestEnforcer is an enforcer that reports as running (no BPF loaded).
func egressTestEnforcer() *NetworkPolicyEnforcer {
	e := NewNetworkPolicyEnforcer("", nil, NewMemTenantRegistry(), nil, nil, nil, true)
	e.running = true
	return e
}

func egressPolicy(t *testing.T, tenant, cidr string) netpolicy.CompiledPolicy {
	return compiled(t, &pb.NetworkPolicy{Tenant: tenant, EgressCidrs: []string{cidr}})
}

func hasEgressIncomplete(st *pb.NetworkPolicyStatus) bool {
	return slices.Contains(st.GetUncoveredPaths(), egressIncompletePath)
}

// A tenant's policy is removed after its last box is gone, and the removal of
// its allow-list entries fails: destinations stay allowed that no policy
// allows. The tenant has no box at all, yet its status must say so.
func TestEgressIncomplete_FailedRemovalWithNoRunningBox(t *testing.T) {
	e := egressTestEnforcer()
	acme := containerView{Name: "acme-a", Tenant: "acme", TenantID: 1, Ifindex: 11, HasVeth: true, Running: true}
	bob := containerView{Name: "bob-a", Tenant: "bob", TenantID: 2, Ifindex: 21, HasVeth: true, Running: true}
	policies := map[string]netpolicy.CompiledPolicy{
		"acme": egressPolicy(t, "acme", "192.0.2.0/24"),
