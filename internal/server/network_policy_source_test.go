package server

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// GetNetworkPolicyStatus says whether a tenant runs the host's default egress
// allow-list or a stored policy (#2996).
func TestNetworkPolicyStatus_PolicySource(t *testing.T) {
	def, err := netpolicy.DefaultPolicy("acme", netpolicy.DefaultEgressExtras{})
	if err != nil {
		t.Fatal(err)
	}
	views := []containerView{{Name: "acme-container", Tenant: "acme", TenantID: 1, Ifindex: 11, HasVeth: true, Running: true}}
	cases := []struct {
		name     string
		policies map[string]netpolicy.CompiledPolicy
		want     pb.NetworkPolicySource
	}{
		{"host default", map[string]netpolicy.CompiledPolicy{"acme": def}, pb.NetworkPolicySource_NETWORK_POLICY_SOURCE_HOST_DEFAULT},
		{"stored policy", map[string]netpolicy.CompiledPolicy{"acme": compiled(t, &pb.NetworkPolicy{Tenant: "acme", Mode: stLogOnly})}, pb.NetworkPolicySource_NETWORK_POLICY_SOURCE_STORED},
		{"default posture off, no policy", map[string]netpolicy.CompiledPolicy{}, pb.NetworkPolicySource_NETWORK_POLICY_SOURCE_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			applied := appliedPass(t, views, tc.policies, false)
			st := npStatusFor(t, fakeAppliedSource{fresh(applied["acme"])}, false, "acme")
			if st.GetPolicySource() != tc.want {
				t.Errorf("policy_source = %v, want %v", st.GetPolicySource(), tc.want)
			}
		})
