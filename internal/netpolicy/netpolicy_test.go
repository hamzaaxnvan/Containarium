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
	if got.EgressCIDRs[0].String() != "1.2.3.0/24" || got.EgressCIDRs[1].String() != "10.0.0.0/8" {
		t.Errorf("CIDRs not masked/sorted: %v", got.EgressCIDRs)
	}
	// Domains lowercased, trailing dot stripped, trimmed, deduped → 2 unique.
	if len(got.EgressDomains) != 2 || got.EgressDomains[0] != "api.github.com" || got.EgressDomains[1] != "pypi.org" {
		t.Errorf("domains not normalized/deduped: %v", got.EgressDomains)
	}
	if got.Mode != pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE || got.LogOnly {
		t.Errorf("ENFORCE should not be log-only: mode=%v logOnly=%v", got.Mode, got.LogOnly)
	}
}

func TestCompile_ModeDefaultsToLogOnly(t *testing.T) {
	for _, m := range []pb.NetworkPolicyMode{
		pb.NetworkPolicyMode_NETWORK_POLICY_MODE_UNSPECIFIED,
		pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY,
	} {
		got, err := Compile(&pb.NetworkPolicy{Tenant: "t", Mode: m})
		if err != nil {
			t.Fatalf("Compile(mode=%v): %v", m, err)
		}
		if got.Mode != pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY || !got.LogOnly {
			t.Errorf("mode=%v should resolve to LOG_ONLY + LogOnly=true, got mode=%v logOnly=%v", m, got.Mode, got.LogOnly)
		}
	}
}

