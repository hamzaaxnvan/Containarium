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

