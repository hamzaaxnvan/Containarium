package server

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"

	"github.com/footprintai/containarium/internal/metrics/cloudexport"
	"github.com/footprintai/containarium/internal/metrics/platformstats"
	"github.com/footprintai/containarium/pkg/core/backup"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// serverMetricsSources is the production cloudexport.Sources adapter: it
// reuses the daemon's existing collection paths (Incus GetSystemResources
// for the host snapshot, the container Manager for the count and the
// per-container map) rather than reimplementing any of it. Constructed
// when export is enabled; the Incus client it holds is a persistent
// connection reused across ticks, mirroring how the Manager holds one.
type serverMetricsSources struct {
	manager *container.Manager
	client  *incus.Client
}

// newServerMetricsSources builds the adapter, opening the one Incus
// client it reuses for host-resource reads. An Incus dial failure here
// fails enable-time, before any collector starts.
func newServerMetricsSources(manager *container.Manager) (*serverMetricsSources, error) {
	client, err := incus.New()
	if err != nil {
		return nil, fmt.Errorf("connect to Incus for metrics export: %w", err)
	}
	return &serverMetricsSources{manager: manager, client: client}, nil
}

// SystemResources returns the host snapshot projected down to exactly the
// fields the allowlisted host series need. The container count comes from
// the Manager's List (same source GetSystemInfo counts from), not a
// second Incus round trip.
func (s *serverMetricsSources) SystemResources(ctx context.Context) (*cloudexport.SystemResources, error) {
	res, err := s.client.GetSystemResources()
	if err != nil {
		return nil, fmt.Errorf("get system resources: %w", err)
	}
	containers, err := s.manager.List()
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	return &cloudexport.SystemResources{
		CPULoad1Min:      res.CPULoad1Min,
		CPULoad5Min:      res.CPULoad5Min,
		CPULoad15Min:     res.CPULoad15Min,
		MemoryUsedBytes:  res.UsedMemoryBytes,
		MemoryTotalBytes: res.TotalMemoryBytes,
		DiskUsedBytes:    res.UsedDiskBytes,
		DiskTotalBytes:   res.TotalDiskBytes,
		ContainerCount:   int64(len(containers)),
	}, nil
}

// Hostname returns this host's Incus server name — the same value
// GetSystemInfo reports as Hostname — for the exported series' hostname
// label. Empty (not an error) when server info is unavailable, so a
// transient Incus hiccup at enable time doesn't block export.
func (s *serverMetricsSources) Hostname() string {
	info, err := s.client.GetServerInfo()
	if err != nil || info == nil {
		return ""
	}
	return info.Environment.ServerName
}

