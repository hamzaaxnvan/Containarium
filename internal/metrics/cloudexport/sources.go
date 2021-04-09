package cloudexport

import (
	"context"
	"time"

	"github.com/footprintai/containarium/internal/metrics/platformstats"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// SystemResources is the host-level snapshot the CloudExportCollector
// turns into the allowlisted host series (#1070). It is a deliberately
// small projection of the daemon's richer incus.SystemResources — only
// the fields that map to an exported gauge appear here, so the cost
// surface (custom metrics are billed per ingested sample) stays
// reviewable in one struct. A field here without a matching instrument
// in collector.go, or vice versa, is a review smell.
type SystemResources struct {
	// CPU load averages, straight from /proc/loadavg via the daemon's
	// existing GetSystemResources path.
	CPULoad1Min  float64
	CPULoad5Min  float64
	CPULoad15Min float64

	// Host memory, in bytes.
	MemoryUsedBytes  int64
	MemoryTotalBytes int64

	// Aggregate host disk (sum across storage pools), in bytes.
	DiskUsedBytes  int64
	DiskTotalBytes int64
