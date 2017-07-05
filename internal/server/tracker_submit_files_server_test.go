package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/tracker"
	"github.com/footprintai/containarium/internal/tracker/submit"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeFilePusher is a submit.FilePusher test double recording the
// FilesSpec it was called with.
type fakeFilePusher struct {
	called  bool
	gotSpec submit.FilesSpec
	result  submit.PushResult
	err     error
}

func (f *fakeFilePusher) PushFiles(_ context.Context, spec submit.FilesSpec) (submit.PushResult, error) {
	f.called = true
	f.gotSpec = spec
	return f.result, f.err
}

func validFilesReq(user string) *pb.SubmitTrackerChangeFilesRequest {
	return &pb.SubmitTrackerChangeFilesRequest{
		Username: user, Connection: "default", Issue: 7, Title: "Fix the thing",
		Files: []*pb.TrackerChangeFile{{Path: "docs/a.md", Content: "hello\n"}},
	}
}

// setUpFilesConnection is setUpWriterConnection with an OPERATOR context
// (tracker:write, no run_id claim) — the caller this RPC exists for —
// and the file pusher swapped for a fake.
func setUpFilesConnection(t *testing.T, user string, provider *fakeWriterProvider, pusher *fakeFilePusher) (*ContainerServer, context.Context) {
	t.Helper()
	s, _ := setUpWriterConnection(t, user, provider)
	s.submitFilePusher = pusher
	return s, kmsKeyTestCtx(user, "member", "tracker:write")
}

func TestSubmitTrackerChangeFiles_AuthAndArgs(t *testing.T) {
	tests := []struct {
		name string
		s    *ContainerServer
		ctx  context.Context
		mut  func(*pb.SubmitTrackerChangeFilesRequest)
		want codes.Code
	}{
		{"no auth", &ContainerServer{}, context.Background(), nil, codes.Unauthenticated},
		{"read scope", &ContainerServer{}, kmsKeyTestCtx("alice", "member", "tracker:read"), nil, codes.PermissionDenied},
		{"no tracker store", &ContainerServer{}, kmsKeyTestCtx("alice", "member", "tracker:write"), nil, codes.Unavailable},
		{"cross tenant", &ContainerServer{trackerStore: mustTestTrackerStore(t)}, kmsKeyTestCtx("mallory", "member", "tracker:write"), nil, codes.PermissionDenied},
		{"missing issue", &ContainerServer{trackerStore: mustTestTrackerStore(t)}, kmsKeyTestCtx("alice", "member", "tracker:write"),
			func(r *pb.SubmitTrackerChangeFilesRequest) { r.Issue = 0 }, codes.InvalidArgument},
		{"missing title", &ContainerServer{trackerStore: mustTestTrackerStore(t)}, kmsKeyTestCtx("alice", "member", "tracker:write"),
			func(r *pb.SubmitTrackerChangeFilesRequest) { r.Title = "" }, codes.InvalidArgument},
		{"no files", &ContainerServer{trackerStore: mustTestTrackerStore(t)}, kmsKeyTestCtx("alice", "member", "tracker:write"),
			func(r *pb.SubmitTrackerChangeFilesRequest) { r.Files = nil }, codes.InvalidArgument},
		{"bad path", &ContainerServer{trackerStore: mustTestTrackerStore(t)}, kmsKeyTestCtx("alice", "member", "tracker:write"),
			func(r *pb.SubmitTrackerChangeFilesRequest) { r.Files[0].Path = "../escape" }, codes.InvalidArgument},
		{"over size cap", &ContainerServer{trackerStore: mustTestTrackerStore(t)}, kmsKeyTestCtx("alice", "member", "tracker:write"),
			func(r *pb.SubmitTrackerChangeFilesRequest) {
				r.Files[0].Content = strings.Repeat("x", submit.DefaultMaxChangeFilesBytes+1)
			}, codes.InvalidArgument},
		{"bad head branch", &ContainerServer{trackerStore: mustTestTrackerStore(t)}, kmsKeyTestCtx("alice", "member", "tracker:write"),
			func(r *pb.SubmitTrackerChangeFilesRequest) { r.HeadBranch = "a..b" }, codes.InvalidArgument},
		{"bad base ref", &ContainerServer{trackerStore: mustTestTrackerStore(t)}, kmsKeyTestCtx("alice", "member", "tracker:write"),
			func(r *pb.SubmitTrackerChangeFilesRequest) { r.BaseRef = "-x" }, codes.InvalidArgument},
