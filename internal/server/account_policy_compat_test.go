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
	views := []containerView{{Tenant: "account:workload", TenantID: 2}, {Tenant: "workload", TenantID: 1}}
	policies, err := e.planPolicies(ctx, views)
	if err != nil {
		t.Fatal(err)
	}
	account := policies["account:workload"]
	if account.LogOnly || account.Tenant != "account:workload" || len(account.EgressCIDRs) != 1 || len(account.AllowFromTenants) != 0 {
		t.Fatalf("unsafe fallback: %+v", account)
	}
	if incus.ResolveTenant("account:workload", "", "") == incus.ResolveTenant("workload", "", "") {
		t.Fatal("enforcement identities collided")
	}
	if err := store.Set(ctx, &pb.NetworkPolicy{Tenant: "account:workload", AllowFromTenants: []string{"peer"}}); err != nil {
		t.Fatal(err)
	}
	policies, err = e.planPolicies(ctx, views)
	if err != nil {
