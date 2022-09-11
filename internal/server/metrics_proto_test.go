package server

import (
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// toProtoMetrics is the last hop before the wire. A field dropped here reaches
// every consumer — REST, gRPC, the MCP get_metrics tool, and peer-forwarded
// responses — as a zero, which for the throttling counters reads as
// "not reported" and silently restores the #1573 blind spot.
func TestToProtoMetricsCarriesEveryField(t *testing.T) {
	in := &incus.ContainerMetrics{
		Name:             "alice-container",
		CPUUsageSeconds:  1234,
		MemoryUsageBytes: 2048,
		MemoryLimitBytes: 4096,
		DiskUsageBytes:   8192,
		NetworkRxBytes:   16,
		NetworkTxBytes:   32,
		ProcessCount:     7,
		CPUNrPeriods:     40031,
		CPUNrThrottled:   12847,
		CPUThrottledUsec: 98765432,
	}

	got := toProtoMetrics(in)

	checks := []struct {
		field string
		got   int64
		want  int64
	}{
