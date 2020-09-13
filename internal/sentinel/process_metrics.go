package sentinel

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"syscall"
)

// Sentinel process-health metrics (#1351).
//
// The /metrics endpoint published four sentinel_* series describing the
// BACKEND's preemption state and nothing at all about the sentinel process
// itself. So when #1349 leaked ~18 MB/day for 27 consecutive days on the most
// availability-critical VM in the fleet, there was no series to graph and
// nothing to alert on: the first signal anyone got was the OOM kill, and it
// arrived on a hypervisor CPU graph pointing at the wrong resource.
//
// The argument that made preemption a sentinel-owned signal (#514 — the
// on-spot monitoring stack dies with the spot, so the always-on sentinel is
// the only thing that can report) applies unchanged to the sentinel's own
// process health. It was simply never extended that far.
//
// Names follow the Prometheus/client_golang conventions
// (process_resident_memory_bytes, go_goroutines, …) even though nothing here
// imports client_golang — the repo hand-rolls its exposition, and matching the
// standard names is what lets an off-the-shelf dashboard or alert rule work
// against this endpoint without translation.
//
// RSS is first among equals: the Go heap can look flat while RSS climbs
// (goroutine stacks, mmap'd regions, cgo), and RSS is what the OOM killer
// scores. A rule like
//
//	deriv(process_resident_memory_bytes[6h]) > 0
//
// sustained over a day would have opened #1349 around week one instead of at
// the outage.

// runtimeStats is what the Go runtime knows about itself. Portable.
type runtimeStats struct {
	Goroutines     int
	HeapAllocBytes uint64
	SysBytes       uint64
}

// procStats is what the OS knows about the process. Requires /proc, so it is
// Linux-only — Available is false everywhere else, and callers must then omit
// the series entirely rather than publish zeros.
type procStats struct {
	Available    bool
	RSSBytes     uint64
	VirtualBytes uint64
	OpenFDs      uint64
	MaxFDs       uint64
}

// collectRuntimeStats samples the Go runtime.
//
// Uses runtime/metrics rather than runtime.ReadMemStats: ReadMemStats stops
// the world, and this endpoint is on the request path of a process whose job
// is forwarding live traffic. runtime/metrics samples without a global pause.
func collectRuntimeStats() runtimeStats {
	const (
		heapObjects = "/memory/classes/heap/objects:bytes"
		totalMapped = "/memory/classes/total:bytes"
	)
	samples := []metrics.Sample{{Name: heapObjects}, {Name: totalMapped}}
	metrics.Read(samples)

	rt := runtimeStats{Goroutines: runtime.NumGoroutine()}
