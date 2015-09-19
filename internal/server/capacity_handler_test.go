package server

import (
	"context"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The capacity RPCs (#680) are admin-only: they read/mutate a fleet-level
// scheduling signal, not a tenant resource.

func TestAdvertiseCapacity_RejectsNonAdmin(t *testing.T) {
	srv := &ContainerServer{}
	ctx := auth.ContextWithTestSubject(context.Background(), "alice", "user")
	if _, err := srv.AdvertiseCapacity(ctx, &pb.AdvertiseCapacityRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("non-admin must be denied; got %v (%v)", status.Code(err), err)
	}
}

func TestWithdrawCapacity_RejectsNonAdmin(t *testing.T) {
	srv := &ContainerServer{}
	ctx := auth.ContextWithTestSubject(context.Background(), "alice", "user")
	if _, err := srv.WithdrawCapacity(ctx, &pb.WithdrawCapacityRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("non-admin must be denied; got %v (%v)", status.Code(err), err)
	}
}

func TestGetCapacityHeadroom_RejectsNonAdmin(t *testing.T) {
	srv := &ContainerServer{}
	ctx := auth.ContextWithTestSubject(context.Background(), "alice", "user")
	if _, err := srv.GetCapacityHeadroom(ctx, &pb.GetCapacityHeadroomRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("non-admin must be denied; got %v (%v)", status.Code(err), err)
	}
}

// Advertise → Get → Withdraw (twice) exercises the lifecycle and the
// idempotence of withdraw. The nil manager makes hostStateSnapshot return a
