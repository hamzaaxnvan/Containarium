package server

import (
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/poolcapacity"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// SetStoragePoolSource wires the pool capacity monitor's snapshot into
// GetSystemInfo (#2823).
func (s *ContainerServer) SetStoragePoolSource(fn func() poolcapacity.Snapshot) {
	s.storagePools = fn
}

// storagePoolCapacities projects the monitor's last snapshot onto
// SystemInfo.storage_pools. It never calls Incus, so GetSystemInfo stays as
// fast as before; nil when no monitor is wired or it has not read yet.
func (s *ContainerServer) storagePoolCapacities() []*pb.StoragePoolCapacity {
	if s.storagePools == nil {
		return nil
	}
	snap := s.storagePools()
	if len(snap.Pools) == 0 {
		return nil
	}
	out := make([]*pb.StoragePoolCapacity, 0, len(snap.Pools))
	for _, p := range snap.Pools {
		out = append(out, &pb.StoragePoolCapacity{
			Pool:       p.Name,
			Driver:     storageDriverToProto(incus.StorageDriver(p.Driver)),
