package server

import (
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AppServer scope-gate coverage. Apps are functionally
// container-shaped (DeployApp creates a routed
// application backed by a container), so they reuse the
// containers:read|write scopes rather than minting a new
// pair. Same pattern as scope_gate_test.go.

func TestAppServer_DeployApp_RejectsMissingScope(t *testing.T) {
	srv := &AppServer{}
	ctx := tenantWithScopes("alice", auth.ScopeContainersRead) // read-only
	_, err := srv.DeployApp(ctx, &pb.DeployAppRequest{
