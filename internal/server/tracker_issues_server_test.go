package server

import (
	"context"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetTrackerIssue_NoAuthContext(t *testing.T) {
	s := &ContainerServer{}
	_, err := s.GetTrackerIssue(context.Background(), &pb.GetTrackerIssueRequest{Username: "alice", Connection: "default", Number: 1})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// TestGetTrackerIssue_TrackerAdminScopeInsufficient pins the inverse
// separation from TestSetTrackerConnection_TrackerWriteScopeInsufficient:
// tracker:admin (connection CRUD) does not imply tracker:read (the
// verbs). An operator token scoped only for managing connections cannot
// also read issues through it.
func TestGetTrackerIssue_TrackerAdminScopeInsufficient(t *testing.T) {
	s := &ContainerServer{}
	ctx := kmsKeyTestCtx("alice", "member", "tracker:admin")
	_, err := s.GetTrackerIssue(ctx, &pb.GetTrackerIssueRequest{Username: "alice", Connection: "default", Number: 1})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", status.Code(err))
	}
}

func TestListTrackerIssues_NoAuthContext(t *testing.T) {
	s := &ContainerServer{}
	_, err := s.ListTrackerIssues(context.Background(), &pb.ListTrackerIssuesRequest{Username: "alice", Connection: "default"})
	if status.Code(err) != codes.Unauthenticated {
