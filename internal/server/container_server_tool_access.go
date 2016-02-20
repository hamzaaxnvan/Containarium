package server

import (
	"context"
	"log"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/toolaccess"
	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SetContainerToolAccess records the tool-access level for platform-started
// (unattended) agent runs on a container (#2944). It only records the level on
// the Incus container config (user.containarium.tool_access), so it survives a
// daemon restart with no separate store and is read back on list/get; applying
// it to the run path is a separate change. UNSPECIFIED removes the key (back to
// "none recorded"). Like the other per-container RPCs, req.Name carries the
// bare username and the handler resolves <username>-container via boxes().Get.
func (s *ContainerServer) SetContainerToolAccess(ctx context.Context, req *pb.SetContainerToolAccessRequest) (*pb.SetContainerToolAccessResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeContainersWrite); err != nil {
		return nil, err
	}
