//go:build !windows && !containarium_client

package cmd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Sentinel unit memory bounds (#1350, corrected by #1454).
//
// #1349 bursts ~330 MB in ~35 min on backend loss until the sentinel held
// 565 MB anon-RSS on a 1 GB host. Nothing bounded it, so the KERNEL resolved
// it — 36 minutes into a full-host stall (73.91% iowait, load 32.46, 12 tasks
// blocked in D), long after the box had stopped serving usefully.
// `Restart=always` did its job and the sentinel came back.
//
// MemoryMax is the hard stop; paired with Restart=always it costs a restart
// measured in seconds instead of a 36-minute apex outage. That half was right.
//
// MemoryHigh BELOW MemoryMax was not. #1454: a soft cap only helps if reclaim
// can make progress, and progress needs something reclaimable — page cache, or
// anon plus swap. These hosts have NO swap, and the #1349 burst is
// anon-dominated, so file cache is squeezed to ~0 within minutes and reclaim
// has nothing left to do. The cgroup then parks between MemoryHigh and
// MemoryMax and is throttled indefinitely: it never reaches the hard cap, so
// Restart=always never fires, and systemd reports active(running) throughout.
//
// Measured during the #1454 outage: memory.events high=556267 with max=0 and
// oom_kill=0, PSI full avg300=86.10 (86% of wall-clock with every task in the
// cgroup frozen), anon 271 MB against file 0.36 MB, and pgscan 79.4M against
// pgsteal 2.4M — a 3% reclaim success rate, i.e. futile scanning. ~90 minutes
// of total edge outage, ended by manual intervention, where the OOM-and-restart
// path it replaced self-recovered in seconds.
//
// So the dead band is the bug, and the old 35%/50% defaults had one: a ~154 MB
// window (~358 MB to ~512 MB on a 1 GB host) in which any burst that comes to
// rest is stuck. Note that neither burst measured so far lands there — 271 MB
// sits below it and 565 MB above it — so the shipped defaults are a LATENT
// stall window, not the cause of the #1454 outage; that host carried a tighter
// operator drop-in (256M/384M) and the burst came to rest inside its band. The
// window is still indefensible: nothing chooses where a burst stops, and the
// only reason to keep a band is a reclaim path that these hosts do not have.
//
// Defaulting MemoryHigh to MemoryMax removes the window — the cgroup is still
// reclaimed locally at the threshold rather than by global reclaim evicting the
// host's page cache, and memory.events "high" still increments as an early
// signal, but a burst that cannot be reclaimed now proceeds to the cap and is
// restarted instead of hanging.
//
// A gap remains legitimate where reclaim CAN make progress, so it is allowed
// on hosts that have swap and rejected on hosts that do not.
//
// Defaults are PERCENTAGES so one value works across the 1 GB (e2-micro) and
// 2 GB (e2-small, per #770) sentinel hosts without per-host tuning. They are
// overridable because BYOC and larger sentinels exist.

const (
	// defaultSentinelMemoryHigh deliberately EQUALS defaultSentinelMemoryMax.
	// Any lower value opens the #1454 throttle band, in which a swapless host
	// stalls forever instead of restarting. Reclaim still runs at the
	// threshold; what is removed is the window to get stuck in.
	defaultSentinelMemoryHigh = "50%"

	// defaultSentinelMemoryMax caps at ~512 MB on a 1 GB host, deliberately
	// BELOW the 565 MB the #1349 burst reached — a ceiling above that would
	// never have fired and would be decoration.
	defaultSentinelMemoryMax = "50%"
)

// sentinelUnitConfig is everything that varies in the generated unit.
type sentinelUnitConfig struct {
	SpotVM     string
	Zone       string
	Project    string
	MemoryHigh string
	MemoryMax  string

	// HostHasSwap reports whether the target host can page anonymous memory
	// out. It decides whether a MemoryHigh below MemoryMax is a safety valve
	// or the #1454 livelock, so it is passed in rather than read here: this
	// file stays pure and testable without /proc, and the install path is the
	// only thing that touches the host.
	HostHasSwap bool
}

// systemdMemoryPattern matches the value forms systemd accepts for
// MemoryHigh=/MemoryMax=: a percentage, or a byte count with an optional
// binary suffix. Note "M" not "MB" — systemd rejects the latter.
var systemdMemoryPattern = regexp.MustCompile(`^(\d+)(%|K|M|G|T|)$`)

// validateSystemdMemoryValue rejects anything systemd would refuse, so a typo
// surfaces at install time rather than as a unit that will not start.
func validateSystemdMemoryValue(v string) error {
	if v == "" {
		return fmt.Errorf("empty memory value")
	}
	if v == "infinity" {
		return nil
	}
	m := systemdMemoryPattern.FindStringSubmatch(v)
	if m == nil {
		return fmt.Errorf("invalid memory value %q: want a percentage (\"35%%\"), a size (\"512M\", \"1G\"), or \"infinity\"", v)
	}
	if m[2] == "%" {
		pct, err := strconv.Atoi(m[1])
		if err != nil {
			return fmt.Errorf("invalid memory value %q", v)
		}
		if pct < 1 || pct > 100 {
			return fmt.Errorf("memory percentage %q must be between 1%% and 100%%", v)
		}
	}
	return nil
}

// percentValue returns the numeric part of a percentage value, erroring if v
// is not one.
func percentValue(v string) (int, error) {
	m := systemdMemoryPattern.FindStringSubmatch(v)
	if m == nil || m[2] != "%" {
		return 0, fmt.Errorf("%q is not a percentage", v)
	}
	return strconv.Atoi(m[1])
}
