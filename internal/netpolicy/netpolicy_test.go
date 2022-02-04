package netpolicy

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestCompile_OK(t *testing.T) {
	got, err := Compile(&pb.NetworkPolicy{
		Tenant:           "alice",
		AllowIntraTenant: true,
		EgressCidrs:      []string{"10.0.0.0/8", "1.2.3.4/24", "10.0.0.0/8"}, // dup + non-network
		EgressDomains:    []string{"API.github.com", "api.github.com.", " pypi.org "},
		Mode:             pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got.Tenant != "alice" || !got.AllowIntraTenant {
		t.Errorf("tenant/allow_intra wrong: %+v", got)
	}
	// 1.2.3.4/24 masked → 1.2.3.0/24; 10.0.0.0/8 deduped → 2 unique.
	if len(got.EgressCIDRs) != 2 {
		t.Fatalf("want 2 CIDRs, got %d: %v", len(got.EgressCIDRs), got.EgressCIDRs)
	}
