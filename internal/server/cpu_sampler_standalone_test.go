package server

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/metrics"
	"github.com/footprintai/containarium/internal/threatdetect"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// fakeCPUSource is a fake Incus client for the standalone sampler (#2645):
// a fixed container list, a per-tick cumulative CPU counter per container,
// per-container errors, and a call counter on GetContainerMetrics.
type fakeCPUSource struct {
	containers []incus.ContainerInfo
	usage      map[string]int64 // current cumulative CPUUsageSeconds
	failing    map[string]bool
	listErr    error
	seq        map[string][]int64 // when set, each read pops the next counter value

	mu    sync.Mutex
	calls map[string]int
}

func (f *fakeCPUSource) ListContainers() ([]incus.ContainerInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.containers, nil
}

func (f *fakeCPUSource) GetContainerMetrics(name string) (*incus.ContainerMetrics, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[name]++
	if f.failing[name] {
		return nil, errors.New("incus: boom")
	}
	if q := f.seq[name]; len(q) > 0 {
		f.seq[name] = q[1:]
		return &incus.ContainerMetrics{Name: name, CPUUsageSeconds: q[0]}, nil
	}
	return &incus.ContainerMetrics{Name: name, CPUUsageSeconds: f.usage[name]}, nil
}

func cpuBox(name, state, ip, cpu string) incus.ContainerInfo {
	return incus.ContainerInfo{Name: name, State: state, IPAddress: ip, CPU: cpu}
}

// TestStandaloneCPUSampler pins the standalone ticker's contract (#2645):
// deltas identical to the collector path for the same counter series, one
// metrics call per running container per tick, stopped containers never
// queried, and one container's Incus error skips only that container and is
// counted.
func TestStandaloneCPUSampler(t *testing.T) {
