package server

import (
	"context"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"testing"
)

func TestAccountPolicyFallbackPreservesEnforcementWithoutJoiningSharedDomain(t *testing.T) {
	ctx := context.Background()
	store := NewMemNetworkPolicyStore()
	legacy := &pb.NetworkPolicy{Tenant: "workload", Mode: pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE, EgressCidrs: []string{"192.0.2.0/24"}, AllowFromTenants: []string{"peer"}, AllowIntraTenant: true}
	if err := store.Set(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	e := NewNetworkPolicyEnforcer("", store, NewMemTenantRegistry(), nil, nil, nil, true)
