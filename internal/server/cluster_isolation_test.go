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
