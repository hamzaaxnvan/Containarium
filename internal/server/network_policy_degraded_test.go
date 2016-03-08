package server

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cloudv1 "github.com/footprintai/containarium/pkg/pb/containarium/cloud/v1"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #3131: while the network-policy store is the in-memory fallback (Postgres
// configured but unreachable at startup), operator mutations are refused at
// the API boundary so nothing is silently lost on restart. Reads still work,
// and the daemon's own in-process writers keep writing to memory.

const degradedMsg = "network-policy store is degraded: running on the in-memory fallback (Postgres unavailable at startup); nothing was changed"

// newDegradedNPServer is a server on the in-memory fallback, with one policy
// and one signature already in it so reads and deletes have something to hit.
func newDegradedNPServer(t *testing.T) *NetworkPolicyServer {
	t.Helper()
	s := newNPServer()
	s.SetSignatureStore(NewMemNetworkPolicySignatureStore())
	ctx := npAdminCtx()
	if _, err := s.SetNetworkPolicy(ctx, &pb.SetNetworkPolicyRequest{Policy: &pb.NetworkPolicy{Tenant: "alice"}}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	// Set never writes deny rules (MutateDenyRules owns them, #660).
	if _, err := s.PatchNetworkPolicyDenyRules(ctx, &pb.PatchNetworkPolicyDenyRulesRequest{
		Tenant: "alice", Add: []*pb.NetworkPolicyDenyRule{{Cidr: "9.9.9.9/32"}},
	}); err != nil {
		t.Fatalf("seed deny rule: %v", err)
	}
	if _, err := s.SetNetworkPolicySignature(ctx, &pb.SetNetworkPolicySignatureRequest{Signature: &pb.NetworkPolicySignature{
		Name: "sig1", Pattern: "evil", Enabled: true,
	}}); err != nil {
		t.Fatalf("seed signature: %v", err)
	}
	s.SetDurable(false)
	return s
}

// degradedMutations is every operator write the decision on #3131 gates.
func degradedMutations() []struct {
	name string
	call func(context.Context, *NetworkPolicyServer) error
} {
	return []struct {
		name string
		call func(context.Context, *NetworkPolicyServer) error
	}{
		{"SetNetworkPolicy", func(ctx context.Context, s *NetworkPolicyServer) error {
			_, err := s.SetNetworkPolicy(ctx, &pb.SetNetworkPolicyRequest{Policy: &pb.NetworkPolicy{Tenant: "bob"}})
			return err
		}},
		{"PatchNetworkPolicyDenyRules", func(ctx context.Context, s *NetworkPolicyServer) error {
			_, err := s.PatchNetworkPolicyDenyRules(ctx, &pb.PatchNetworkPolicyDenyRulesRequest{
