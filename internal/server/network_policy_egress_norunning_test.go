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
