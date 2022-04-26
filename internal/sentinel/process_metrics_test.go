package sentinel

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// #1351: the sentinel's /metrics published four sentinel_* series and nothing
// about the process itself, so #1349's ~18 MB/day growth ran for 27 days with
// no series to graph and nothing to alert on. These tests pin the series that
// close that gap — above all resident memory, which is what the OOM killer
// actually acts on.

// fakeProcStats is a snapshot with recognisable values, so a test failure says
// which field got mis-rendered rather than just "some number is wrong".
func fakeProcStats() procStats {
	return procStats{
		Available:    true,
		RSSBytes:     565600 * 1024, // the anon-rss from #1349's OOM line
		VirtualBytes: 2753240 * 1024,
		OpenFDs:      37,
		MaxFDs:       65536, // LimitNOFILE from the unit
	}
}

func fakeRuntimeStats() runtimeStats {
	return runtimeStats{
		Goroutines:     42,
		HeapAllocBytes: 123456789,
		SysBytes:       987654321,
	}
}

func TestRenderProcessMetrics_PublishesLeakDetectionSeries(t *testing.T) {
	out := renderProcessMetrics(fakeRuntimeStats(), fakeProcStats())

	for _, want := range []string{
		// The series that would have caught #1349. RSS is first among equals:
		// the Go heap can look flat while RSS climbs (stacks, mmap, cgo), and
		// RSS is what the OOM killer scores.
		"# TYPE process_resident_memory_bytes gauge",
		"process_resident_memory_bytes 5.791744e+08",
		"# TYPE process_virtual_memory_bytes gauge",
		"process_virtual_memory_bytes 2.81931776e+09",
		// A goroutine leak is a common cause of a memory leak; an fd leak is
		// the other classic way this daemon dies (LimitNOFILE=65536).
		"# TYPE go_goroutines gauge",
		"go_goroutines 42",
		"# TYPE process_open_fds gauge",
		"process_open_fds 37",
		"# TYPE process_max_fds gauge",
		"process_max_fds 65536",
		// Heap detail, for telling "which kind of growth" once RSS moves.
		"# TYPE go_memstats_heap_alloc_bytes gauge",
		"go_memstats_heap_alloc_bytes 1.23456789e+08",
		"# TYPE go_memstats_sys_bytes gauge",
		"go_memstats_sys_bytes 9.87654321e+08",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("process metrics missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// TestRenderProcessMetrics_OmitsUnavailableProcSeries is the one that matters
// for correctness of alerting. /proc exists on the Linux hosts the sentinel
// runs on but not on a developer's macOS box. When the read fails the series
// must be ABSENT, never zero: a hard-coded
// `process_resident_memory_bytes 0` reads as "this process uses no memory",
// which silences exactly the alert this issue exists to enable. An absent
// series is detectable with absent(); a lying zero is not.
func TestRenderProcessMetrics_OmitsUnavailableProcSeries(t *testing.T) {
	out := renderProcessMetrics(fakeRuntimeStats(), procStats{Available: false})

	for _, unwanted := range []string{
		"process_resident_memory_bytes",
		"process_virtual_memory_bytes",
		"process_open_fds",
		"process_max_fds",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("emitted %q with no /proc available — a zero here would silence the leak alert\n--- got ---\n%s", unwanted, out)
		}
	}

	// Runtime series come from the Go runtime and are portable, so they must
	// still be there — losing /proc must not cost us everything.
	if !strings.Contains(out, "go_goroutines 42") {
		t.Errorf("runtime series dropped along with the proc series\n--- got ---\n%s", out)
	}
}
