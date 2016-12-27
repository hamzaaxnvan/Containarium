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
