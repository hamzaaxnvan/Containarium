package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/zfskey"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func dryRunCtx() context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(), "alice", nil, []string{auth.ScopeContainersWrite})
}

// dryRunNoPanic calls CreateContainer and turns a panic into a failed
// assertion: a bare ContainerServer has no manager, so any path that reaches
// provisioning would dereference nil.
func dryRunNoPanic(t *testing.T, srv *ContainerServer, req *pb.CreateContainerRequest) (*pb.CreateContainerResponse, error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CreateContainer reached provisioning on a dry run: %v", r)
		}
	}()
	return srv.CreateContainer(dryRunCtx(), req)
}

// #2925: a valid dry run passes every pre-flight check and returns before
// anything is provisioned, so a daemon with no manager at all answers it.
func TestCreateContainer_DryRun_ValidRequestProvisionsNothing(t *testing.T) {
	resp, err := dryRunNoPanic(t, &ContainerServer{}, &pb.CreateContainerRequest{
		Username: "alice", Stack: "nodejs", TtlSeconds: 3600, DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry run of a valid request failed: %v", err)
	}
	if !resp.GetDryRun() {
		t.Error("response.dry_run = false: the caller cannot tell nothing was created")
	}
	if resp.GetContainer() != nil {
		t.Errorf("a dry run returned a container: %v", resp.GetContainer())
	}
}

// Every rejection a real create gives, a dry run gives, with the same code and
// text: that is what makes it safe to delete the old box after it passes.
func TestCreateContainer_DryRun_RejectsExactlyWhatARealCreateRejects(t *testing.T) {
	cases := []struct {
		name string
		req  *pb.CreateContainerRequest
		want codes.Code
	}{
		{"unknown stack", &pb.CreateContainerRequest{Username: "alice", Stack: "no-such-stack"}, codes.InvalidArgument},
		{"ttl out of range", &pb.CreateContainerRequest{Username: "alice", TtlSeconds: 1 << 40}, codes.InvalidArgument},
		{"negative idle stop", &pb.CreateContainerRequest{Username: "alice", IdleStopMinutes: -1}, codes.InvalidArgument},
		{"contradictory auto-sleep", &pb.CreateContainerRequest{Username: "alice", DisableAutoSleep: true, IdleStopMinutes: 5}, codes.InvalidArgument},
		{"negative delete-after-stopped", &pb.CreateContainerRequest{Username: "alice", DeleteAfterStoppedSeconds: -1}, codes.InvalidArgument},
		{"foreign tenant id", &pb.CreateContainerRequest{Username: "alice", TenantId: "some-other-tenant"}, codes.InvalidArgument},
		{"encrypted without key custody", &pb.CreateContainerRequest{Username: "alice", Encrypted: true}, codes.FailedPrecondition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			real, realErr := dryRunNoPanic(t, &ContainerServer{}, tc.req)
			if realErr == nil || real != nil {
