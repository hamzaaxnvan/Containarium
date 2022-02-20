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
	}
	_, err = store.PatchTenantAccess(ctx, []*pb.TenantAccessChange{{Destination: "testing", RemoveSources: []string{"allinone"}, ExpectedRevision: PolicyRevision(after)}})
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := store.Get(ctx, "testing")
	if !proto.Equal(restored, before) {
		t.Fatal("revoke failed to restore policy exactly")
	}
}
func TestTenantAccessBidirectionalAtomicConflict(t *testing.T) {
	store := NewMemNetworkPolicyStore()
	ctx := context.Background()
	_ = store.Set(ctx, &pb.NetworkPolicy{Tenant: "testing"})
	_, err := store.PatchTenantAccess(ctx, []*pb.TenantAccessChange{{Destination: "allinone", AddSources: []string{"testing"}, ExpectedRevision: "absent"}, {Destination: "testing", AddSources: []string{"allinone"}, ExpectedRevision: "absent"}})
	if !errors.Is(err, ErrTenantAccessConflict) {
		t.Fatalf("wanted revision conflict: %v", err)
	}
	if _, err = store.Get(ctx, "allinone"); !errors.Is(err, ErrNetworkPolicyNotFound) {
		t.Fatal("partial bidirectional write committed")
	}
	existing, _ := store.Get(ctx, "testing")
	out, err := store.PatchTenantAccess(ctx, []*pb.TenantAccessChange{{Destination: "allinone", AddSources: []string{"testing"}, ExpectedRevision: "absent"}, {Destination: "testing", AddSources: []string{"allinone"}, ExpectedRevision: PolicyRevision(existing)}})
	if err != nil || len(out) != 2 {
		t.Fatalf("bidirectional mutation: %v", err)
	}
}
func TestTenantAccessConcurrentPreviewHasOneWinner(t *testing.T) {
	store := NewMemNetworkPolicyStore()
	ctx := context.Background()
	_ = store.Set(ctx, &pb.NetworkPolicy{Tenant: "testing"})
	old, _ := store.Get(ctx, "testing")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, src := range []string{"first", "second"} {
		wg.Add(1)
		go func(src string) {
			defer wg.Done()
			_, err := store.PatchTenantAccess(ctx, []*pb.TenantAccessChange{{Destination: "testing", AddSources: []string{src}, ExpectedRevision: PolicyRevision(old)}})
			errs <- err
		}(src)
	}
	wg.Wait()
