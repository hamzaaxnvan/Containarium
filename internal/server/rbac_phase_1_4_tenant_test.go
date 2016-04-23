package server

import (
	"context"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Phase 1.4 follow-up — handlers whose authz key is a
// container name. Each must:
//  1) reject a tenant who doesn't own the named container
//  2) reject a tenant when no name is given AND the empty-name
//     path would otherwise leak cross-tenant data
//  3) pass for the owning tenant (subject == owner)
//  4) pass for admin in both shapes
//
// As with the admin-only tests, the server is constructed
// with nil dependencies — a passing test means the gate
// fires before any nil-deref, i.e. structurally.

func tenantCtx(name string) context.Context {
	return auth.ContextWithTestSubject(context.Background(), name, "user")
}

func adminCtx() context.Context {
	return auth.ContextWithTestSubject(context.Background(), "ops", auth.RoleAdmin)
}

// --- TrafficServer (4 RPCs) ---

func TestTrafficGetConnections_RejectsOtherTenant(t *testing.T) {
	srv := &TrafficServer{}
	_, err := srv.GetConnections(tenantCtx("alice"), &pb.GetConnectionsRequest{ContainerName: "bob-container"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("got %v want PermissionDenied", err)
	}
}

func TestTrafficGetConnections_RejectsSystemContainerForTenant(t *testing.T) {
	srv := &TrafficServer{}
	_, err := srv.GetConnections(tenantCtx("alice"), &pb.GetConnectionsRequest{ContainerName: "caddy"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("got %v want PermissionDenied", err)
	}
}

func TestTrafficGetConnectionSummary_RejectsOtherTenant(t *testing.T) {
	srv := &TrafficServer{}
	_, err := srv.GetConnectionSummary(tenantCtx("alice"), &pb.GetConnectionSummaryRequest{ContainerName: "bob-container"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("got %v want PermissionDenied", err)
	}
}

func TestTrafficQueryHistory_RejectsOtherTenant(t *testing.T) {
	srv := &TrafficServer{}
	_, err := srv.QueryTrafficHistory(tenantCtx("alice"), &pb.QueryTrafficHistoryRequest{ContainerName: "bob-container"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("got %v want PermissionDenied", err)
	}
}

func TestTrafficAggregates_RejectsOtherTenant(t *testing.T) {
	srv := &TrafficServer{}
	_, err := srv.GetTrafficAggregates(tenantCtx("alice"), &pb.GetTrafficAggregatesRequest{ContainerName: "bob-container"})
