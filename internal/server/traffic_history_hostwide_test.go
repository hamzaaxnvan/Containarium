package server

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2884: a host-wide history query reveals every tenant's connection history,
// so it is admin-only (same rule as SubscribeTraffic with no container).
func TestTrafficQueryHistory_HostWideRequiresAdmin(t *testing.T) {
	srv := &TrafficServer{}
	req := &pb.QueryTrafficHistoryRequest{
		StartTime: timestamppb.New(time.Now().Add(-time.Hour)),
		DestIp:    "203.0.113.9",
	}
	_, err := srv.QueryTrafficHistory(tenantCtx("alice"), req)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("tenant host-wide query: got %v want PermissionDenied", err)
	}
	// An admin passes authz and reaches the store check: this server has no
	// collector, so FailedPrecondition is exactly "got past authorization".
	_, err = srv.QueryTrafficHistory(adminCtx(), req)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("admin host-wide query: got %v, want FailedPrecondition (reached the store check)", err)
	}
