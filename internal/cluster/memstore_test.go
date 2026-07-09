package cluster

// SetNodeGroupTarget (#1882). The both-impls round-trip lives in the
// tagged store_integration_test.go; this is the untagged half so the
// fast lane also fails when a concurrent caller for one group can
// clobber a different group's already-committed target.

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func newTestCluster(t *testing.T, s *MemStore, owner, name string, groups []NodeGroup) {
	t.Helper()
	ctx := context.Background()
	if err := s.Create(ctx, &Cluster{
		ID: uuid.NewString(), Owner: owner, Name: name,
		State: StateProvisioning, NodeGroups: groups,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}
