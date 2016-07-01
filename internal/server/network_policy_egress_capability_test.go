package server

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// appliedPassOnObject is appliedPass for a loaded object that does or does not
// read the egress_mode byte (#2987), returning the tenants whose split mode the
// object cannot honour.
func appliedPassOnObject(t *testing.T, views []containerView, policies map[string]netpolicy.CompiledPolicy, supportsEgressMode bool) (map[string]tenantApplied, map[uint32]bool) {
	t.Helper()
	plan := planReconcile(views, policies, true)
	ignored := foldEgressModeForObject(&plan, supportsEgressMode)
	loaded := make(map[int]bool, len(plan.vethPolicy))
	for ifindex := range plan.vethPolicy {
		loaded[ifindex] = true
	}
	return appliedFromPass(views, plan, loaded, policies, nil, nil), ignored
}

// An object built before #2974 ignores egress_mode and applies `mode` to the
// allow-list, so the status reports `mode` as the effective egress mode — for
// both splits — and never the requested one. A current object keeps the split.
func TestNetworkPolicyStatus_ObjectWithoutEgressModeReportsMode(t *testing.T) {
	views := []containerView{{Name: "acme-container", Tenant: "acme", TenantID: 1, Ifindex: 11, HasVeth: true, Running: true}}
	for _, tc := range []struct {
		name               string
		mode, egress       pb.NetworkPolicyMode
		supportsEgressMode bool
		wantEgress         pb.NetworkPolicyMode
		wantIgnored        bool
	}{
		{"old object, log_only/enforce", stLogOnly, stEnforce, false, stLogOnly, true},
		{"old object, enforce/log_only", stEnforce, stLogOnly, false, stEnforce, true},
		{"old object, no split", stEnforce, stUnset, false, stEnforce, false},
		{"current object, log_only/enforce", stLogOnly, stEnforce, true, stEnforce, false},
		{"current object, enforce/log_only", stEnforce, stLogOnly, true, stLogOnly, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policies := map[string]netpolicy.CompiledPolicy{
				"acme": compiled(t, &pb.NetworkPolicy{Tenant: "acme", Mode: tc.mode, EgressMode: tc.egress}),
			}
			applied, ignored := appliedPassOnObject(t, views, policies, tc.supportsEgressMode)
			st := npStatusFor(t, fakeAppliedSource{fresh(applied["acme"])}, true, "acme")
			if st.GetEffectiveMode() != tc.mode {
				t.Errorf("effective mode = %v, want %v", st.GetEffectiveMode(), tc.mode)
			}
			if st.GetEffectiveEgressMode() != tc.wantEgress {
				t.Errorf("effective egress mode = %v, want %v", st.GetEffectiveEgressMode(), tc.wantEgress)
			}
			if ignored[1] != tc.wantIgnored {
				t.Errorf("tenant reported as ignored = %v, want %v (ignored=%v)", ignored[1], tc.wantIgnored, ignored)
			}
		})
