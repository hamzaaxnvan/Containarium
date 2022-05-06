package server

import (
	"context"
	"errors"
	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"sync"
	"testing"
	"time"
)

func richAccessPolicy(key string) *pb.NetworkPolicy {
	return &pb.NetworkPolicy{Tenant: key, AllowIntraTenant: true, EgressCidrs: []string{"10.0.0.0/8"}, EgressDomains: []string{"example.com"}, Mode: pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE, AllowMetadata: true, Source: "cloud-managed", EgressPreset: pb.EgressPreset_EGRESS_PRESET_ALLOW_LIST_ONLY, EgressMode: pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY, DenyRules: []*pb.NetworkPolicyDenyRule{{Cidr: "10.2.3.4/32", Port: 443, Note: "keep"}}}
}
func TestTenantAccessDirectionPreservationAndRevocation(t *testing.T) {
	store := NewMemNetworkPolicyStore()
	ctx := context.Background()
	old := richAccessPolicy("testing")
	_ = store.Set(ctx, old)
	// Set owns allow-policy, seed independently managed deny rules through its API.
	_, _ = store.MutateDenyRules(ctx, "testing", func([]*pb.NetworkPolicyDenyRule) ([]*pb.NetworkPolicyDenyRule, error) { return old.DenyRules, nil })
	before, _ := store.Get(ctx, "testing")
	change := &pb.TenantAccessChange{Destination: "testing", AddSources: []string{"allinone"}, ExpectedRevision: PolicyRevision(before)}
	out, err := store.PatchTenantAccess(ctx, []*pb.TenantAccessChange{change})
	if err != nil {
		t.Fatal(err)
	}
	after := out[0].Policy
	if len(after.AllowFromTenants) != 1 || after.AllowFromTenants[0] != "allinone" {
		t.Fatal("authorization applied to wrong destination")
	}
	actual := proto.Clone(after).(*pb.NetworkPolicy)
	actual.AllowFromTenants = nil
	if !proto.Equal(actual, before) {
		t.Fatal("ingress patch clobbered another security field")
	}
	if _, err := store.Get(ctx, "allinone"); !errors.Is(err, ErrNetworkPolicyNotFound) {
		t.Fatal("one direction implicitly granted reverse access")
