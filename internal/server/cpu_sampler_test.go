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
