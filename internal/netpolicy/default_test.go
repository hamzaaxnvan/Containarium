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
