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
	t0 := time.Unix(1_760_140_800, 0)
	series := map[string][]int64{ // cumulative counter per tick
		"a": {100, 160, 220, 5, 20},
		"b": {0, 30, 60, 90, 120},
	}
	cases := []struct {
		name       string
		containers []incus.ContainerInfo
		failing    map[string]bool
		wantNames  []string // containers expected in emitted samples
		wantErrors uint64
		wantCalls  map[string]int
	}{
		{
			name:       "all running sampled, deltas match collector path",
			containers: []incus.ContainerInfo{cpuBox("a", "Running", "10.0.0.5", "250m"), cpuBox("b", "Running", "10.0.0.6", "2")},
			wantNames:  []string{"a", "b"},
			wantCalls:  map[string]int{"a": 5, "b": 5},
		},
		{
			name:       "stopped container never queried",
			containers: []incus.ContainerInfo{cpuBox("a", "Running", "10.0.0.5", "250m"), cpuBox("b", "Stopped", "", "2")},
			wantNames:  []string{"a"},
			wantCalls:  map[string]int{"a": 5},
		},
		{
			name:       "incus error on one container skips it, others sampled, error counted",
			containers: []incus.ContainerInfo{cpuBox("a", "Running", "10.0.0.5", "250m"), cpuBox("b", "Running", "10.0.0.6", "2")},
			failing:    map[string]bool{"b": true},
			wantNames:  []string{"a"},
			wantErrors: 5,
			wantCalls:  map[string]int{"a": 5, "b": 5},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := &fakeCPUSource{containers: tc.containers, usage: map[string]int64{}, failing: tc.failing}
			var standalone [][]threatdetect.CPUSample
			s := NewStandaloneCPUSampler(src, func(smp []threatdetect.CPUSample) { standalone = append(standalone, smp) })

			// Reference: the collector path, fed the same readings the
			// collector would build for the containers the standalone
			// sampler could read.
			var reference [][]threatdetect.CPUSample
			ref := NewCPUSampler(func(smp []threatdetect.CPUSample) { reference = append(reference, smp) })

			for i := range series["a"] {
				at := t0.Add(time.Duration(i) * StandaloneCPUSampleInterval)
				var readings []metrics.ContainerCPUReading
				for _, ct := range tc.containers {
					src.usage[ct.Name] = series[ct.Name][i]
					if ct.State == "Running" && !tc.failing[ct.Name] {
						readings = append(readings, metrics.ContainerCPUReading{Name: ct.Name, IPAddress: ct.IPAddress, CPULimit: ct.CPU, CPUUsageSeconds: series[ct.Name][i]})
					}
				}
				s.Tick(at)
				ref.Observe(at, readings)
			}

			if !reflect.DeepEqual(standalone, reference) {
				t.Fatalf("standalone samples differ from collector path:\n got  %+v\n want %+v", standalone, reference)
			}
			if len(standalone) == 0 {
