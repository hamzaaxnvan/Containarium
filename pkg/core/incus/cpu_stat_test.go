package incus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCPUStat(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    cpuThrottle
		wantOK  bool
	}{
		{
			name: "cgroup v2 cpu.stat with a throttled container",
			content: `usage_usec 12345678
user_usec 9000000
system_usec 3345678
nr_periods 40031
nr_throttled 12847
throttled_usec 98765432
`,
			want:   cpuThrottle{NrPeriods: 40031, NrThrottled: 12847, ThrottledUsec: 98765432},
			wantOK: true,
		},
		{
			name: "unthrottled container still reports periods",
			content: `usage_usec 500
nr_periods 8123
nr_throttled 0
throttled_usec 0
`,
			want:   cpuThrottle{NrPeriods: 8123},
			wantOK: true,
		},
		{
			// A cgroup with no CPU bandwidth limit set omits the burst/throttle
			// keys entirely on some kernels — usage-only is not a parse failure,
			// but it carries no throttling signal.
			name:    "usage-only cpu.stat has no throttling keys",
			content: "usage_usec 500\nuser_usec 100\nsystem_usec 400\n",
			want:    cpuThrottle{},
			wantOK:  false,
		},
		{
			name:    "empty file",
			content: "",
			want:    cpuThrottle{},
			wantOK:  false,
		},
		{
			// Defensive: a malformed value must not poison the other counters.
			name:    "malformed value is skipped, siblings survive",
			content: "nr_periods 100\nnr_throttled notanumber\nthrottled_usec 42\n",
			want:    cpuThrottle{NrPeriods: 100, ThrottledUsec: 42},
			wantOK:  true,
		},
		{
			// cgroup v1 spells the time counter in nanoseconds under a different
			// key; we normalize it to microseconds so callers see one unit.
			name:    "cgroup v1 throttled_time (ns) normalizes to usec",
			content: "nr_periods 10\nnr_throttled 3\nthrottled_time 5000000\n",
