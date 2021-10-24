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

// TestSetNodeGroupTarget_OnlyTouchesTheNamedGroup pins the property
// that makes a concurrent call for a different group safe: this call
// has no externally-held snapshot of the OTHER groups to carry
// forward, so there is nothing for it to clobber them with.
func TestSetNodeGroupTarget_OnlyTouchesTheNamedGroup(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	newTestCluster(t, s, "e2etenant", "lane", []NodeGroup{
		{Name: "small", MinNodes: 1, MaxNodes: 3, TargetNodes: 1},
		{Name: "medium", MinNodes: 0, MaxNodes: 2, TargetNodes: 1},
	})

	if err := s.SetNodeGroupTarget(ctx, "e2etenant", "lane", "medium", 0); err != nil {
		t.Fatalf("SetNodeGroupTarget: %v", err)
	}

	got, err := s.Get(ctx, "e2etenant", "lane")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, g := range got.NodeGroups {
		switch g.Name {
		case "medium":
			if g.TargetNodes != 0 {
				t.Errorf("medium.TargetNodes = %d, want 0", g.TargetNodes)
