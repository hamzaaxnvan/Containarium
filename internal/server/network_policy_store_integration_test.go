//go:build integration

package server

import (
	"context"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// egress_mode survives the Postgres store: Set → Get/List read it back, a
// policy that never set it reads back UNSPECIFIED ("follow mode"), and a deny
// rule mutation leaves it untouched.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/server/ -run NetworkPolicyStore_PostgresEgressMode
func TestNetworkPolicyStore_PostgresEgressMode(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS network_policies`); err != nil {
		t.Fatalf("reset network_policies: %v", err)
	}
	s, err := NewPostgresNetworkPolicyStore(ctx, pool)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	enforce := pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE
	logOnly := pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY
	if err := s.Set(ctx, &pb.NetworkPolicy{Tenant: "alice", Mode: enforce, EgressMode: logOnly}); err != nil {
		t.Fatalf("Set alice: %v", err)
	}
	if err := s.Set(ctx, &pb.NetworkPolicy{Tenant: "bob", Mode: enforce}); err != nil {
		t.Fatalf("Set bob: %v", err)
	}

	got, err := s.Get(ctx, "alice")
