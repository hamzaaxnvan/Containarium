package incus

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// cgroupV2Root is where the unified cgroup v2 hierarchy is mounted on the host
// the daemon runs on.
const cgroupV2Root = "/sys/fs/cgroup"

// cpuThrottle holds the CFS bandwidth-throttling counters a cgroup reports in
// cpu.stat.
//
// These are what separate a *throttled* box from an *idle* one. Cumulative
// CPU-seconds alone reads as the same low number in both cases, which is why a
// starved tenant has no observable signal saying so today (#1573). A box whose
// nr_throttled climbs is hitting its own CFS quota — the
// limits.cpu.allowance ceiling written by cpu_limits.go. A box with flat
// nr_throttled and low usage is either genuinely idle or losing the host CPU
// race to its neighbours; the box's own cgroup cannot tell those apart, so that
// distinction needs the host's committed:physical ratio as well (#1571).
//
// All three are cumulative over the container's lifetime, matching how
// CPUUsageSeconds already behaves — callers take deltas between two reads.
type cpuThrottle struct {
	NrPeriods     int64 // CFS periods elapsed
	NrThrottled   int64 // periods in which the container was throttled
	ThrottledUsec int64 // total time spent throttled, in microseconds
}

// parseCPUStat parses the contents of a cgroup cpu.stat file.
//
// The bool reports whether any throttling counter was found — not whether the
// container was throttled. A cgroup with no CPU bandwidth limit omits the
// throttling keys entirely on some kernels and reports usage only; that is a
// legitimate "no signal available" answer rather than a parse error, and the
// caller leaves the fields unset instead of publishing a misleading zero.
//
// Unparseable values are skipped individually so one malformed line cannot
// discard the counters around it.
func parseCPUStat(content string) (cpuThrottle, bool) {
	var t cpuThrottle
	found := false

	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "nr_periods":
			t.NrPeriods, found = n, true
		case "nr_throttled":
			t.NrThrottled, found = n, true
		case "throttled_usec":
			t.ThrottledUsec, found = n, true
		case "throttled_time":
			// cgroup v1 spells the same counter in nanoseconds. Normalize so
			// callers see one unit regardless of the host's hierarchy.
			t.ThrottledUsec, found = n/1000, true
		}
	}

	return t, found
}

// cgroupV2PathFromProc extracts the unified-hierarchy cgroup path from the
// contents of a /proc/<pid>/cgroup file, relative to the cgroup root.
