//go:build !containarium_client

package server

// Unit coverage for ClusterServer (#1413): scope gates, tenant isolation
// (IDOR), lifecycle semantics, and the kubeconfig seam — all against the
// in-memory store (the Postgres impl is held to the same contract in
// internal/cluster's integration suite).

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/cluster"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func clusterTestServer() *ClusterServer {
	return NewClusterServer(cluster.NewMemStore())
}

// scopedCtx is a tenant context restricted to exactly the given scopes
// (unlike tenantCtx, whose nil scopes claim means "unrestricted").
func scopedCtx(username string, scopes ...string) context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(), username, []string{"user"}, scopes)
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if status.Code(err) != want {
		t.Fatalf("got error %v (code %v), want code %v", err, status.Code(err), want)
	}
}

func mustCreate(t *testing.T, s *ClusterServer, ctx context.Context, name string) *pb.Cluster {
	t.Helper()
	resp, err := s.CreateCluster(ctx, &pb.CreateClusterRequest{Name: name})
	if err != nil {
		t.Fatalf("CreateCluster(%s): %v", name, err)
	}
	return resp.Cluster
}

// --- scope gates -------------------------------------------------------

func TestClusterServer_ScopeGates(t *testing.T) {
	s := clusterTestServer()
	readOnly := scopedCtx("alice", auth.ScopeClustersRead)
	writeOnly := scopedCtx("alice", auth.ScopeClustersWrite)
	unrelated := scopedCtx("alice", auth.ScopeContainersWrite)

	// Writes need clusters:write.
	if _, err := s.CreateCluster(readOnly, &pb.CreateClusterRequest{Name: "demo"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Create with read-only scope = %v, want PermissionDenied", err)
	}
	if _, err := s.CreateCluster(unrelated, &pb.CreateClusterRequest{Name: "demo"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Create with unrelated scope = %v, want PermissionDenied", err)
	}
	if _, err := s.CreateCluster(writeOnly, &pb.CreateClusterRequest{Name: "demo"}); err != nil {
		t.Fatalf("Create with write scope: %v", err)
	}

	// Reads need clusters:read (write does not imply read).
	if _, err := s.GetCluster(writeOnly, &pb.GetClusterRequest{Name: "demo"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Get with write-only scope = %v, want PermissionDenied", err)
	}
	if _, err := s.GetCluster(readOnly, &pb.GetClusterRequest{Name: "demo"}); err != nil {
		t.Fatalf("Get with read scope: %v", err)
	}
	if _, err := s.ListClusters(unrelated, &pb.ListClustersRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("List with unrelated scope = %v, want PermissionDenied", err)
	}
	if _, err := s.GetClusterStatus(readOnly, &pb.GetClusterStatusRequest{Name: "demo"}); err != nil {
		t.Fatalf("Status with read scope: %v", err)
	}

	// Kubeconfig is cluster-admin material: gated on clusters:write,
	// so a read-only (inspection) token cannot take control of the
	// cluster's workloads.
	if _, err := s.GetClusterKubeconfig(readOnly, &pb.GetClusterKubeconfigRequest{Name: "demo"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Kubeconfig with read-only scope = %v, want PermissionDenied", err)
	}

