package server

import (
	"context"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/coreguard"
	"github.com/footprintai/containarium/internal/nicguard"
	"github.com/footprintai/containarium/internal/safecast"
	"github.com/footprintai/containarium/internal/tenantguard"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// SetGuards hands the two NIC-ACL reconcilers to the network server so
// GetNetworkGuardStatus can report them. Either may be nil (no incus
// client on this host); its status then reads as mode UNSPECIFIED.
func (s *NetworkServer) SetGuards(core *coreguard.Reconciler, tenant *tenantguard.Reconciler) {
	s.coreGuard = core
	s.tenantGuard = tenant
}

// GetNetworkGuardStatus reports both guards' last-pass snapshots. Admin only:
// the entries name every tenant container on the host.
func (s *NetworkServer) GetNetworkGuardStatus(ctx context.Context, _ *pb.GetNetworkGuardStatusRequest) (*pb.NetworkGuardStatus, error) {
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}
	out := &pb.NetworkGuardStatus{}
	if s.coreGuard != nil {
		out.Core = coreGuardStatusToProto(s.coreGuard.Status())
	}
