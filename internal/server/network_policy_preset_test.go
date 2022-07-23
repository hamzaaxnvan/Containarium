package server

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// --- egress preset: storage and enforcer fold-in (#2440) ---

func TestEgressPreset_SurvivesSetGetAndTheStoreClone(t *testing.T) {
	s := newNPServer()
	ctx := npAdminCtx()
	if _, err := s.SetNetworkPolicy(ctx, &pb.SetNetworkPolicyRequest{Policy: &pb.NetworkPolicy{
		Tenant:       "acme",
		Mode:         pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE,
		EgressPreset: pb.EgressPreset_EGRESS_PRESET_ALLOW_LIST_ONLY,
	}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetNetworkPolicy(ctx, &pb.GetNetworkPolicyRequest{Tenant: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetPolicy().GetEgressPreset() != pb.EgressPreset_EGRESS_PRESET_ALLOW_LIST_ONLY {
		t.Fatalf("preset lost across set/get: %v", got.GetPolicy().GetEgressPreset())
	}
	// A later set without the preset clears it, like every other field of the
	// allow-policy: set declares the whole allow-policy.
	if _, err := s.SetNetworkPolicy(ctx, &pb.SetNetworkPolicyRequest{Policy: &pb.NetworkPolicy{Tenant: "acme"}}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetNetworkPolicy(ctx, &pb.GetNetworkPolicyRequest{Tenant: "acme"})
	if got.GetPolicy().GetEgressPreset() != pb.EgressPreset_EGRESS_PRESET_UNSPECIFIED {
		t.Errorf("set without a preset must clear it, got %v", got.GetPolicy().GetEgressPreset())
	}
	// An unknown preset is refused at the API, not stored.
	_, err = s.SetNetworkPolicy(ctx, &pb.SetNetworkPolicyRequest{Policy: &pb.NetworkPolicy{Tenant: "acme", EgressPreset: pb.EgressPreset(42)}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("unknown preset: code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestCompiledPolicies_PresetAllowsTheResolverImplicitly(t *testing.T) {
	store := NewMemNetworkPolicyStore()
	ctx := context.Background()
	for _, p := range []*pb.NetworkPolicy{
		{Tenant: "locked", EgressCidrs: []string{"203.0.113.0/24"}, EgressPreset: pb.EgressPreset_EGRESS_PRESET_ALLOW_LIST_ONLY},
		{Tenant: "plain", EgressCidrs: []string{"203.0.113.0/24"}},
	} {
		if err := store.Set(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	e := NewNetworkPolicyEnforcer("", store, NewMemTenantRegistry(), nil, nil, nil, false)
	e.ctx = ctx
	e.SetImplicitGateway(netip.MustParseAddr("10.100.0.1"))

	got, err := e.compiledPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	has := func(tenant, cidr string) bool {
		for _, p := range got[tenant].EgressCIDRs {
			if p == netip.MustParsePrefix(cidr) {
				return true
			}
		}
		return false
	}
	if !has("locked", "10.100.0.1/32") || !has("locked", "203.0.113.0/24") {
		t.Errorf("preset tenant egress = %v, want the listed CIDR plus the resolver", got["locked"].EgressCIDRs)
	}
	if has("plain", "10.100.0.1/32") {
		t.Errorf("a tenant without the preset must not get the resolver implicitly: %v", got["plain"].EgressCIDRs)
	}
	// What the operator wrote is untouched in the store: `get` shows it as set.
	stored, _ := store.Get(ctx, "locked")
	if len(stored.GetEgressCidrs()) != 1 {
		t.Errorf("stored policy must not carry the implicit entry: %v", stored.GetEgressCidrs())
	}
}

func TestCompiledPolicies_PresetWithUnknownGatewayAddsNothing(t *testing.T) {
	store := NewMemNetworkPolicyStore()
	ctx := context.Background()
	_ = store.Set(ctx, &pb.NetworkPolicy{Tenant: "locked", EgressPreset: pb.EgressPreset_EGRESS_PRESET_ALLOW_LIST_ONLY})
	e := NewNetworkPolicyEnforcer("", store, NewMemTenantRegistry(), nil, nil, nil, false)
	e.ctx = ctx
	got, err := e.compiledPolicies(ctx)
	if err != nil {
		t.Fatal(err)
