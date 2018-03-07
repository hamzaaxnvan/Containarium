package server

import (
	"net/netip"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/metrics"
	"github.com/footprintai/containarium/internal/threatdetect"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// CPUSampler turns the metrics collector's cumulative per-container
// CPUUsageSeconds into per-tick core rates for the threat-detection engine
// (#2635). It is a hook on the collector (Collector.SetCPUHook), so the
// mining-behavior rule adds no Incus polling of its own.
type CPUSampler struct {
	emit func([]threatdetect.CPUSample)

	mu   sync.Mutex
	prev map[string]cpuReading
}
