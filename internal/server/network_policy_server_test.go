package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func npAdminCtx() context.Context {
	return auth.ContextWithTestSubject(context.Background(), "ops", auth.RoleAdmin)
}

// newNPServer pins the kernel probe to "supported" so status tests do not
// depend on the kernel of the machine running them.
func newNPServer() *NetworkPolicyServer {
	s := NewNetworkPolicyServer(NewMemNetworkPolicyStore())
	s.kernelUnsupported = func() bool { return false }
	return s
}

func TestNetworkPolicy_SetNormalizesAndRoundTrips(t *testing.T) {
	s := newNPServer()
	ctx := npAdminCtx()

	set, err := s.SetNetworkPolicy(ctx, &pb.SetNetworkPolicyRequest{Policy: &pb.NetworkPolicy{
		Tenant:           "alice",
		AllowIntraTenant: true,
		EgressCidrs:      []string{"1.2.3.4/24", "10.0.0.0/8", "1.2.3.4/24"}, // non-network + dup
		EgressDomains:    []string{"API.github.com", "api.github.com."},
		// mode unspecified → should normalize to LOG_ONLY
	}})
	if err != nil {
		t.Fatalf("SetNetworkPolicy: %v", err)
	}
	p := set.GetPolicy()
	if len(p.GetEgressCidrs()) != 2 || p.GetEgressCidrs()[0] != "1.2.3.0/24" {
		t.Errorf("CIDRs not normalized/deduped: %v", p.GetEgressCidrs())
	}
	if len(p.GetEgressDomains()) != 1 || p.GetEgressDomains()[0] != "api.github.com" {
		t.Errorf("domains not normalized/deduped: %v", p.GetEgressDomains())
	}
	if p.GetMode() != pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY {
		t.Errorf("mode should default to LOG_ONLY, got %v", p.GetMode())
	}

	got, err := s.GetNetworkPolicy(ctx, &pb.GetNetworkPolicyRequest{Tenant: "alice"})
	if err != nil {
		t.Fatalf("GetNetworkPolicy: %v", err)
	}
	if got.GetPolicy().GetTenant() != "alice" || !got.GetPolicy().GetAllowIntraTenant() {
		t.Errorf("round-trip mismatch: %+v", got.GetPolicy())
	}
}

func TestNetworkPolicy_SetRejectsInvalid(t *testing.T) {
	s := newNPServer()
	_, err := s.SetNetworkPolicy(npAdminCtx(), &pb.SetNetworkPolicyRequest{
		Policy: &pb.NetworkPolicy{Tenant: "alice", EgressCidrs: []string{"not-a-cidr"}},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for bad CIDR, got %v", err)
	}
