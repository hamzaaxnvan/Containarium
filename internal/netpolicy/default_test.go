package netpolicy

import (
	"net/netip"
	"slices"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// The default posture (#2577 stage 1) is observe-only by construction: a
// tenant with no stored policy must never be moved to enforce by it.
func TestDefaultPolicy_IsLogOnlyAllowList(t *testing.T) {
	got, err := DefaultPolicy("acme", DefaultEgressExtras{})
	if err != nil {
		t.Fatalf("DefaultPolicy: %v", err)
	}
	if got.Tenant != "acme" {
		t.Errorf("Tenant = %q, want acme", got.Tenant)
	}
	if !got.LogOnly || got.Mode != pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY {
		t.Errorf("Mode = %v LogOnly = %v, want LOG_ONLY", got.Mode, got.LogOnly)
	}
	if got.EffectiveEgressMode() != pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY {
		t.Errorf("EffectiveEgressMode = %v, want LOG_ONLY", got.EffectiveEgressMode())
	}
	if got.EgressPreset != pb.EgressPreset_EGRESS_PRESET_ALLOW_LIST_ONLY {
		t.Errorf("EgressPreset = %v, want ALLOW_LIST_ONLY so the DNS resolver is implicit", got.EgressPreset)
	}
	if got.AllowMetadata {
		t.Error("the default must not allow the cloud metadata service")
	}
}

func TestDefaultPolicy_CoversTheDocumentedClasses(t *testing.T) {
	got, err := DefaultPolicy("acme", DefaultEgressExtras{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"github.com", "registry.npmjs.org", "pypi.org", "proxy.golang.org", "deb.debian.org"} {
		if !slices.Contains(got.EgressDomains, d) {
			t.Errorf("default allow-list is missing %q", d)
		}
	}
}

