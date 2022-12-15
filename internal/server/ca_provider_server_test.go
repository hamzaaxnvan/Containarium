//go:build !containarium_client

package server

// Contract tests for the externalgrpc CloudProvider implementation
// (#1415): a REAL gRPC server dialed by the client generated from the
// SAME vendored proto — proto drift fails here, not in production.
// The scaling loop is closed against the reconciler rig: a target
// raised over the wire materializes as VMs on the fake host.

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	clustercore "github.com/footprintai/containarium/pkg/core/cluster"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	capb "github.com/footprintai/containarium/pkg/pb/thirdparty/externalgrpc"
)

// caRig serves a CAProviderServer over a real listener, sharing the
// store/manager/reconciler with the cluster rig.
func caRig(t *testing.T) (capb.CloudProviderClient, *ClusterServer, *ClusterReconciler, *stateHost) {
	t.Helper()
	srv, rec, host := testReconcilerRig(t)
	mgr := clustercore.NewManagerWithLoader(host, func() ([]byte, error) { return []byte("k3s-bin"), nil })
	provider := NewCAProviderServer(srv.Store(), mgr)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	capb.RegisterCloudProviderServer(gs, provider)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return capb.NewCloudProviderClient(conn), srv, rec, host
}

// caCtx carries a cluster machine identity the way the production
// transport stamps it (subject k8s-cluster:<owner>/<name>, scope
// clusters:scale).
func caCtx(owner, name string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(),
		auth.MDKeyUsername, CASubject(owner, name),
		auth.MDKeyRoles, "machine",
		auth.MDKeyScopes, auth.ScopeClustersScale,
	)
}

func TestCAProvider_NodeGroupsAndIdentity(t *testing.T) {
	client, srv, _, _ := caRig(t)
	mustCreate(t, srv, tenantCtx("alice"), "demo")

	// The cluster's own identity sees its groups with truthful bounds.
	resp, err := client.NodeGroups(caCtx("alice", "demo"), &capb.NodeGroupsRequest{})
	if err != nil {
		t.Fatalf("NodeGroups: %v", err)
	}
	if len(resp.NodeGroups) != 3 {
		t.Fatalf("groups = %d, want 3 presets", len(resp.NodeGroups))
	}
	byID := map[string]*capb.NodeGroup{}
	for _, g := range resp.NodeGroups {
		byID[g.Id] = g
	}
	small := byID["alice/demo/small"]
	if small == nil || small.MinSize != 1 || small.MaxSize != 3 {
		t.Fatalf("small group = %+v, want min 1 max 3", small)
	}

	// A DIFFERENT cluster's identity cannot touch demo's groups.
	_, err = client.NodeGroupTargetSize(caCtx("alice", "other"), &capb.NodeGroupTargetSizeRequest{Id: "alice/demo/small"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-cluster call = %v, want PermissionDenied", err)
	}

	// A human token (no machine subject) is refused even with wildcard scope.
	human := metadata.AppendToOutgoingContext(context.Background(),
		auth.MDKeyUsername, "alice", auth.MDKeyRoles, auth.RoleAdmin, auth.MDKeyScopes, auth.ScopeWildcard)
	_, err = client.NodeGroups(human, &capb.NodeGroupsRequest{})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("human token = %v, want PermissionDenied", err)
	}

	// Missing scope is refused before anything else.
	noScope := metadata.AppendToOutgoingContext(context.Background(),
		auth.MDKeyUsername, CASubject("alice", "demo"), auth.MDKeyRoles, "machine", auth.MDKeyScopes, auth.ScopeClustersRead)
	_, err = client.NodeGroups(noScope, &capb.NodeGroupsRequest{})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("wrong scope = %v, want PermissionDenied", err)
	}
}

func TestCAProvider_ScaleUpMaterializesThroughReconciler(t *testing.T) {
	client, srv, rec, host := caRig(t)
	ctx := context.Background()
	mustCreate(t, srv, tenantCtx("alice"), "demo")
	rec.ReconcileOnce(ctx) // CP
	rec.ReconcileOnce(ctx) // worker to min (1)

	ts, err := client.NodeGroupTargetSize(caCtx("alice", "demo"), &capb.NodeGroupTargetSizeRequest{Id: "alice/demo/small"})
	if err != nil || ts.TargetSize != 1 {
		t.Fatalf("initial target = %v (%v), want 1 (min)", ts.GetTargetSize(), err)
	}

	// CA raises the target by 2 (pending pods) — bounded at max 3.
	if _, err := client.NodeGroupIncreaseSize(caCtx("alice", "demo"), &capb.NodeGroupIncreaseSizeRequest{Id: "alice/demo/small", Delta: 2}); err != nil {
		t.Fatalf("IncreaseSize: %v", err)
	}
	rec.ReconcileOnce(ctx)
	workers := 0
	for name := range host.vms {
		if strings.Contains(name, "-small-") {
			workers++
		}
	}
	if workers != 3 {
		t.Fatalf("workers after target=3 pass = %d, want 3: %v", workers, vmNames(host))
	}

	// Past max is a loud, typed refusal + a REFUSED event.
	_, err = client.NodeGroupIncreaseSize(caCtx("alice", "demo"), &capb.NodeGroupIncreaseSizeRequest{Id: "alice/demo/small", Delta: 1})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("over-max IncreaseSize = %v, want ResourceExhausted", err)
	}
	st, err := srv.GetClusterStatus(tenantCtx("alice"), &pb.GetClusterStatusRequest{Name: "demo"})
