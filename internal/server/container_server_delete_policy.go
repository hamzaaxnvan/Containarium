package server

import (
	"context"
	"log"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SetContainerDeletePolicy protects or unprotects a container from the daemon's
// UNATTENDED deletion paths (#284) — the ttlsweeper auto-reap and `containarium
// prune`. DELETE_POLICY_PROTECTED stamps user.containarium.delete_policy =
// "protected" (the exact key + value the prune filter and ttlsweeper adapter
// already consult); DELETE_POLICY_UNSPECIFIED removes the key, returning the box
// to the default (eligible for both). A deliberate single-box delete always
// succeeds regardless — this guards against a "clean up leaked boxes" sweep
// taking out a persistent box (e.g. a GitHub Actions runner), not against an
// explicit delete.
//
// Persistence model matches SetContainerTTL: the policy lives on the Incus
// container config, so it survives daemon restart with no separate store and is
// read back on list/get via toProtoContainer. Like the other per-container
// RPCs, req.Name carries the bare username and the handler resolves
// <username>-container via manager.Get.
func (s *ContainerServer) SetContainerDeletePolicy(ctx context.Context, req *pb.SetContainerDeletePolicyRequest) (*pb.SetContainerDeletePolicyResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeContainersWrite); err != nil {
		return nil, err
	}
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	switch req.DeletePolicy {
	case pb.DeletePolicy_DELETE_POLICY_UNSPECIFIED, pb.DeletePolicy_DELETE_POLICY_PROTECTED:
		// ok
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unknown delete_policy %d", req.DeletePolicy)
	}

	username := req.Name
	if err := auth.AuthorizeTenant(ctx, username); err != nil {
		return nil, err
