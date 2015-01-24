// Package storageprobe measures whether one tenant's writeback stalls another
// tenant's fsync on a shared backend.
//
// It exists because the obvious health check is worse than useless here. On
// the failure in #1206 — every tenant rootfs on one filesystem, so every
// tenant on one ext4 jbd2 journal — an idle latency probe reports the affected
// containers as the *fastest* storage in the fleet:
//
//	affected tenant, idle .................... 17 ms
//	physical host, same partition ............ 46 ms
//	a ZFS-backed backend, busy .............. 196 ms
//	affected tenant, under co-tenant load .. 11,885 ms
//
// Only the baseline-to-under-load ratio distinguishes them, which is why
// Classify reports a ratio and why a single number from Probe is not a health
// signal on its own.
//
// The second trap is in the load generator. The trigger is co-tenant
// dirty-page *volume*, not fsync frequency: four tight fsync loops writing
// 4 KiB moved the probe to 32 ms, while eight workers writing 64 MiB buffered
// then syncing produced 11,885 ms. Load therefore writes at volume between
// syncs, and a test pins that ratio so the generator cannot quietly regress
// into the pattern that does not reproduce the bug.
package storageprobe

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Result is one probe run: how many write+fsync operations, and how long they
// took in total.
type Result struct {
	Ops   int
	Total time.Duration
}

// PerOp is the mean latency of a single write+fsync. Zero when nothing ran.
func (r Result) PerOp() time.Duration {
	if r.Ops <= 0 {
		return 0
	}
	return r.Total / time.Duration(r.Ops)
}

// Verdict is the classification of a baseline/under-load pair.
type Verdict int

const (
	// VerdictUnknown means the pair could not be classified — typically an
	// unusable baseline. Deliberately not "isolated": a failed measurement
	// must not read as a clean bill of health.
	VerdictUnknown Verdict = iota

	// VerdictIsolated: co-tenant load did not meaningfully affect this
	// tenant's fsync latency.
	VerdictIsolated

	// VerdictDegraded: a real slowdown under co-tenant load, short of the
	// order-of-magnitude collapse a shared journal produces.
	VerdictDegraded

	// VerdictSevere: fsync latency collapsed under co-tenant load. The
	// signature of tenants sharing one filesystem journal.
	VerdictSevere
)

func (v Verdict) String() string {
	switch v {
	case VerdictIsolated:
		return "isolated"
	case VerdictDegraded:
		return "degraded"
	case VerdictSevere:
		return "severe"
	default:
		return "unknown"
	}
}

// Ratio thresholds. Set from the measurements in #1206: the tight-fsync load
// that did NOT reproduce the stall reached ~1.9x and must stay "isolated", so
// degradedRatio sits well above it; the real stall was ~700x, far past
// severeRatio. The gap between them is deliberately wide — a probe that
// flagged ordinary load variance would be one operators learn to ignore.
const (
	degradedRatio = 3.0
	severeRatio   = 20.0

	// severeMinPerOp is the absolute floor a verdict must also clear before it
	// can be called severe.
	//
	// A ratio has no scale, and that turns out to matter. Benchmarking a
	// migrated backend under four busy co-tenants measured 15.6 ms idle ->
	// 318 ms under load: a 20.4x ratio, but 51x *better* in absolute terms
	// than the host #1206 was filed about (16,179 ms at half the load).
	// Ratio-only logic called that severe, putting a host where builds are
	// fine in the same bucket as one where they stall for 20 seconds.
	//
	// 50 ms per fsync is roughly where a build stops feeling slow and starts
	// looking hung. The #1206 incident sat at ~238 ms per op, far past it;
	// the migrated host sits at ~6 ms, far below.
	severeMinPerOp = 50 * time.Millisecond
)

