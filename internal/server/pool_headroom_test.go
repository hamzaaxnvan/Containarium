package server

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/poolcapacity"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Pool headroom floor (#3128): creates and snapshots succeed above the floor
// and fail closed below it, against a fake pool-capacity source.

// headroomGuard builds a guard over a fixed snapshot: each pool at usedPct of
// 100 GiB, "default" as the daemon's default pool, default floors.
func headroomGuard(usedPct map[string]int64) *poolcapacity.Guard {
	snap := poolcapacity.Snapshot{}
	for name, pct := range usedPct {
		u := incus.StoragePoolUsage{Name: name, Driver: "zfs", UsedBytes: pct << 30, TotalBytes: 100 << 30}
		snap.Pools = append(snap.Pools, poolcapacity.Sample{StoragePoolUsage: u, Level: poolcapacity.Classify(u.UsedBytes, u.TotalBytes)})
	}
