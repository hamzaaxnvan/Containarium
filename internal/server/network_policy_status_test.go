package server

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

const (
	stUnset   = pb.NetworkPolicyMode_NETWORK_POLICY_MODE_UNSPECIFIED
	stLogOnly = pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY
	stEnforce = pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE
)

// fakeAppliedSource is a canned enforcer view for the RPC tests.
type fakeAppliedSource struct{ snap appliedSnapshot }

func (f fakeAppliedSource) AppliedState(string) appliedSnapshot { return f.snap }

// statusNow is the server clock in the RPC tests and the end of the canned
// reconcile pass, so a snapshot from fresh is never stale.
var statusNow = time.Unix(1700000000, 0)

// fresh is a snapshot of a pass that just completed with tenant's state.
func fresh(a tenantApplied) appliedSnapshot {
	return appliedSnapshot{Running: true, Reconciled: true, Found: true, Tenant: a, ReconciledAt: statusNow, Interval: defaultNetPolicyReconcileInterval}
}

// appliedPass runs one reconcile's plan through appliedFromPass, with every
// planned veth loaded unless listed in failed.
func appliedPass(t *testing.T, views []containerView, policies map[string]netpolicy.CompiledPolicy, armed bool, failed ...int) map[string]tenantApplied {
	t.Helper()
	plan := planReconcile(views, policies, armed)
	loaded := make(map[int]bool, len(plan.vethPolicy))
	for ifindex := range plan.vethPolicy {
		loaded[ifindex] = !slices.Contains(failed, ifindex)
	}
	return appliedFromPass(views, plan, loaded, policies, nil, nil)
}

func npStatusFor(t *testing.T, src appliedStateSource, armed bool, tenant string) *pb.NetworkPolicyStatus {
	t.Helper()
	s := newNPServer()
	s.clock = func() time.Time { return statusNow }
	s.SetEnforcementArmed(armed)
	if src != nil {
		s.SetAppliedStateSource(src)
	}
	resp, err := s.GetNetworkPolicyStatus(npAdminCtx(), &pb.GetNetworkPolicyStatusRequest{Tenant: tenant})
	if err != nil {
		t.Fatalf("GetNetworkPolicyStatus: %v", err)
	}
	return resp.GetStatus()
}

// A requested ENFORCE is reported as what the kernel holds: LOG_ONLY on an
// unarmed host, ENFORCE on an armed one — for both modes.
func TestNetworkPolicyStatus_ArmedVsUnarmedHost(t *testing.T) {
	policies := map[string]netpolicy.CompiledPolicy{
		"acme": compiled(t, &pb.NetworkPolicy{Tenant: "acme", Mode: stEnforce}),
	}
	views := []containerView{{Name: "acme-container", Tenant: "acme", TenantID: 1, Ifindex: 11, HasVeth: true, Running: true}}

	for _, tc := range []struct {
		armed bool
		want  pb.NetworkPolicyMode
	}{{false, stLogOnly}, {true, stEnforce}} {
		applied := appliedPass(t, views, policies, tc.armed)
		st := npStatusFor(t, fakeAppliedSource{fresh(applied["acme"])}, tc.armed, "acme")
		if st.GetState() != pb.NetworkPolicyAppliedState_NETWORK_POLICY_APPLIED_STATE_LOADED {
			t.Fatalf("armed=%v: state = %v, want LOADED", tc.armed, st.GetState())
		}
		if st.GetEffectiveMode() != tc.want || st.GetEffectiveEgressMode() != tc.want {
			t.Errorf("armed=%v: effective mode=%v egress=%v, want %v for both", tc.armed, st.GetEffectiveMode(), st.GetEffectiveEgressMode(), tc.want)
		}
		if st.GetEnforcementArmed() != tc.armed {
			t.Errorf("armed=%v: enforcement_armed = %v", tc.armed, st.GetEnforcementArmed())
		}
		if st.GetLastReconciledAt().AsTime() != statusNow.UTC() {
			t.Errorf("armed=%v: last_reconciled_at = %v", tc.armed, st.GetLastReconciledAt().AsTime())
		}
		if st.GetBoxesLoaded() != 1 || st.GetBoxesNotLoaded() != 0 {
			t.Errorf("armed=%v: boxes loaded=%d not=%d, want 1/0", tc.armed, st.GetBoxesLoaded(), st.GetBoxesNotLoaded())
		}
	}
}

// The separate egress mode is reported as loaded.
func TestNetworkPolicyStatus_SeparateEgressMode(t *testing.T) {
	policies := map[string]netpolicy.CompiledPolicy{
		"acme": compiled(t, &pb.NetworkPolicy{Tenant: "acme", Mode: stEnforce, EgressMode: stLogOnly}),
	}
	views := []containerView{{Name: "acme-container", Tenant: "acme", TenantID: 1, Ifindex: 11, HasVeth: true, Running: true}}
	applied := appliedPass(t, views, policies, true)
	st := npStatusFor(t, fakeAppliedSource{fresh(applied["acme"])}, true, "acme")
	if st.GetEffectiveMode() != stEnforce || st.GetEffectiveEgressMode() != stLogOnly {
		t.Errorf("effective mode=%v egress=%v, want ENFORCE/LOG_ONLY", st.GetEffectiveMode(), st.GetEffectiveEgressMode())
	}
}

