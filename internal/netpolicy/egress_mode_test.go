package netpolicy

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// egress_mode is carried through Compile as given: UNSPECIFIED stays
// UNSPECIFIED (it means "follow mode", resolved where the BPF config is
// built), and an explicit value is kept even when it differs from mode.
func TestCompile_EgressModeCarriedAsGiven(t *testing.T) {
	for _, m := range []pb.NetworkPolicyMode{
		pb.NetworkPolicyMode_NETWORK_POLICY_MODE_UNSPECIFIED,
		pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY,
		pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE,
	} {
		got, err := Compile(&pb.NetworkPolicy{
			Tenant:     "t",
			Mode:       pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE,
			EgressMode: m,
		})
		if err != nil {
			t.Fatalf("Compile(egress_mode=%v): %v", m, err)
		}
		if got.EgressMode != m {
			t.Errorf("egress_mode=%v: compiled EgressMode = %v", m, got.EgressMode)
		}
		if got.Mode != pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE {
			t.Errorf("egress_mode=%v must not change Mode, got %v", m, got.Mode)
		}
		if back := got.ToProto().GetEgressMode(); back != m {
			t.Errorf("ToProto egress_mode = %v, want %v", back, m)
		}
	}
}
