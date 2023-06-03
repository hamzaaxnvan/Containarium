package server

import (
	"context"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Scope-route RPCs (#2021). Route CRUD is tracker:admin, like connection
// CRUD: which skill a label starts is an operator decision, never
// something a run's tracker:read/write token can change.

func TestTrackerRoute_RequiresTrackerAdmin(t *testing.T) {
	s := &ContainerServer{}
	tests := []struct {
		name string
		ctx  context.Context
		want codes.Code
	}{
		{"no auth context", context.Background(), codes.Unauthenticated},
		{"tracker:write is not enough", kmsKeyTestCtx("alice", "member", "tracker:write"), codes.PermissionDenied},
		{"tracker:read is not enough", kmsKeyTestCtx("alice", "member", "tracker:read"), codes.PermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.SetTrackerRoute(tt.ctx, &pb.SetTrackerRouteRequest{Username: "alice", Connection: "default", Scope: "product", SkillId: "product-define"})
			if status.Code(err) != tt.want {
				t.Errorf("SetTrackerRoute code = %v, want %v", status.Code(err), tt.want)
			}
			_, err = s.ListTrackerRoutes(tt.ctx, &pb.ListTrackerRoutesRequest{Username: "alice", Connection: "default"})
			if status.Code(err) != tt.want {
				t.Errorf("ListTrackerRoutes code = %v, want %v", status.Code(err), tt.want)
			}
			_, err = s.DeleteTrackerRoute(tt.ctx, &pb.DeleteTrackerRouteRequest{Username: "alice", Connection: "default", Scope: "product"})
			if status.Code(err) != tt.want {
				t.Errorf("DeleteTrackerRoute code = %v, want %v", status.Code(err), tt.want)
			}
		})
	}
}

func TestTrackerRoute_NilStoreUnavailable(t *testing.T) {
	s := &ContainerServer{}
	ctx := kmsKeyTestCtx("alice", "member", "tracker:admin")
	if _, err := s.ListTrackerRoutes(ctx, &pb.ListTrackerRoutesRequest{Username: "alice", Connection: "default"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v, want Unavailable", status.Code(err))
	}
}

// TestTrackerRoute_CrossTenantDenied pins that all three route RPCs share
// requireTrackerRouteAccess's tenant check (#2035's bundled test-gap nit):
// alice acting on bob's tenant without an admin role is denied for
// Set, List and Delete alike, not just Set.
func TestTrackerRoute_CrossTenantDenied(t *testing.T) {
	s := &ContainerServer{trackerStore: mustTestTrackerStore(t)}
	ctx := kmsKeyTestCtx("tracker-route-rpc-alice", "member", "tracker:admin")
	_, err := s.SetTrackerRoute(ctx, &pb.SetTrackerRouteRequest{
		Username: "tracker-route-rpc-bob", Connection: "default", Scope: "product", SkillId: "product-define",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("SetTrackerRoute code = %v, want PermissionDenied", status.Code(err))
	}

	_, err = s.ListTrackerRoutes(ctx, &pb.ListTrackerRoutesRequest{
		Username: "tracker-route-rpc-bob", Connection: "default",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ListTrackerRoutes code = %v, want PermissionDenied", status.Code(err))
	}

	_, err = s.DeleteTrackerRoute(ctx, &pb.DeleteTrackerRouteRequest{
		Username: "tracker-route-rpc-bob", Connection: "default", Scope: "product",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("DeleteTrackerRoute code = %v, want PermissionDenied", status.Code(err))
	}
}

func TestTrackerRoute_CRUDRoundTrip(t *testing.T) {
	store := mustTestTrackerStore(t)
	s := &ContainerServer{trackerStore: store}
	const user = "tracker-route-rpc-crud"
	ctx := context.Background()
