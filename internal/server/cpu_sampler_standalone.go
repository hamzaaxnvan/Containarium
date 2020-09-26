package server

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/footprintai/containarium/internal/metrics"
	"github.com/footprintai/containarium/internal/threatdetect"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// StandaloneCPUSampleInterval is the standalone CPU sampler's tick (#2645).
// It matches the metrics collector's default CollectionInterval, so the
// mining-behavior rule sees the same sample cadence whichever source feeds
// it.
const StandaloneCPUSampleInterval = 30 * time.Second

// CPU sampler sources reported on GetSentryStatus.cpu_sampler (#2645).
const (
	cpuSamplerSourceCollector  = "metrics-collector"
	cpuSamplerNotStartedStatus = "unavailable (not started)"
)

// cpuMetricsSource is the slice of the Incus client the standalone sampler
// reads: the container list (for state, IP and limits.cpu) and the
// per-container cumulative CPU counter. *incus.Client satisfies it.
type cpuMetricsSource interface {
	ListContainers() ([]incus.ContainerInfo, error)
	GetContainerMetrics(name string) (*incus.ContainerMetrics, error)
}

// cpuHookTarget is the metrics collector's CPU hook seam.
// *metrics.Collector satisfies it.
type cpuHookTarget interface {
