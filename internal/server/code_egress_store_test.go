package server

import (
	"context"
	"errors"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// codeEgressStoreContract is the behaviour both CodingToolEgressPolicyStore
// implementations must share. The Postgres half runs it from
// code_egress_store_integration_test.go.
func codeEgressStoreContract(t *testing.T, s CodingToolEgressPolicyStore) {
	t.Helper()
	ctx := context.Background()

	if _, err := s.Get(ctx, "alice"); !errors.Is(err, ErrCodingToolEgressPolicyNotFound) {
		t.Fatalf("Get on an empty store = %v, want ErrCodingToolEgressPolicyNotFound", err)
	}

	first, prev, err := s.Set(ctx, &pb.CodingToolEgressPolicy{
		Tenant: "alice", Mode: pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE,
		EgressCidrs: []string{"192.0.2.0/24"}, EgressDomains: []string{"api.example.com"},
	}, "admin-user")
	if err != nil {
