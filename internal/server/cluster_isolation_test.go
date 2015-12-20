//go:build !containarium_client

package server

// Node isolation contract (#1428): a cluster's isolation class is the
// boundary that contains a tenant kernel exploit. The weaker class
// (CONTAINER) is operator-gated and must be unreachable by omission or
// by a typo — both directions fail closed.
//
// Design: docs/architecture/cluster-container-node-pools.md.

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/footprintai/containarium/internal/cluster"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// A cluster created without asking for an isolation class must come
// back — and be stored — as VM. The safe default is not reachable by
// omission of config; it IS the omission of config.
func TestClusterIsolation_UnspecifiedResolvesToVM(t *testing.T) {
	s := clusterTestServer()
	ctx := tenantCtx("alice")

	resp, err := s.CreateCluster(ctx, &pb.CreateClusterRequest{Name: "demo"})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	if got := resp.Cluster.NodeIsolation; got != pb.NodeIsolation_NODE_ISOLATION_VM {
		t.Fatalf("create response isolation = %v, want VM", got)
	}

	// The store holds the resolved class, not the unset one.
	rec, err := s.store.Get(ctx, "alice", "demo")
	if err != nil {
		t.Fatalf("store Get: %v", err)
	}
	if rec.NodeIsolation != cluster.IsolationVM {
		t.Fatalf("stored isolation = %q, want %q", rec.NodeIsolation, cluster.IsolationVM)
	}

	// And every read surface says so.
	got, err := s.GetCluster(ctx, &pb.GetClusterRequest{Name: "demo"})
	if err != nil {
		t.Fatalf("GetCluster: %v", err)
	}
	if got.Cluster.NodeIsolation != pb.NodeIsolation_NODE_ISOLATION_VM {
		t.Fatalf("GetCluster isolation = %v, want VM", got.Cluster.NodeIsolation)
	}
	st, err := s.GetClusterStatus(ctx, &pb.GetClusterStatusRequest{Name: "demo"})
	if err != nil {
		t.Fatalf("GetClusterStatus: %v", err)
	}
	if st.Cluster.NodeIsolation != pb.NodeIsolation_NODE_ISOLATION_VM {
		t.Fatalf("GetClusterStatus isolation = %v, want VM", st.Cluster.NodeIsolation)
	}
	list, err := s.ListClusters(ctx, &pb.ListClustersRequest{})
	if err != nil || len(list.Clusters) != 1 {
		t.Fatalf("ListClusters = %+v (%v)", list, err)
	}
	if list.Clusters[0].NodeIsolation != pb.NodeIsolation_NODE_ISOLATION_VM {
		t.Fatalf("ListClusters isolation = %v, want VM", list.Clusters[0].NodeIsolation)
