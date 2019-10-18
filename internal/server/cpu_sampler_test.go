package server

import (
	"net/netip"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/metrics"
	"github.com/footprintai/containarium/internal/threatdetect"
)

// TestCPUSamplerDeltas pins the sampler's delta rules (#2635): the first
// reading of a container is dropped (no delta), a steady counter yields its
// rate in cores, a counter that went backwards (restart) is dropped, a
// non-advancing clock is dropped, and a container that disappears starts
// over.
func TestCPUSamplerDeltas(t *testing.T) {
	t0 := time.Unix(1_760_140_800, 0)
	reading := func(name string, usage int64) metrics.ContainerCPUReading {
		return metrics.ContainerCPUReading{Name: name, IPAddress: "10.0.0.5", CPULimit: "250m", CPUUsageSeconds: usage}
	}
	type tick struct {
		at       time.Time
		readings []metrics.ContainerCPUReading
		want     []float64 // cores of emitted samples, in order; nil = no call
	}
	cases := []struct {
		name  string
		ticks []tick
	}{
		{"first sample dropped, then steady 2.0 cores", []tick{
			{t0, []metrics.ContainerCPUReading{reading("box", 100)}, nil},
			{t0.Add(30 * time.Second), []metrics.ContainerCPUReading{reading("box", 160)}, []float64{2.0}},
			{t0.Add(60 * time.Second), []metrics.ContainerCPUReading{reading("box", 220)}, []float64{2.0}},
		}},
		{"counter reset on restart is dropped", []tick{
			{t0, []metrics.ContainerCPUReading{reading("box", 1000)}, nil},
			{t0.Add(30 * time.Second), []metrics.ContainerCPUReading{reading("box", 5)}, nil},
