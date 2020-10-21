package server

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// Local backend liveness (#2317, builds on #920).
//
// Placement and ListBackends ask "may this daemon's own backend take new
// work?". The answer used to be one probe per call that listed EVERY instance
// and then asked Incus for its server info, under a fixed 3s budget, failing
// closed on a timeout. On a host running many instances under CPU pressure the
// instance list alone can outlast 3s, so a slow-but-alive incusd made the whole
// host unplaceable, and every create into the pool failed for as long as the
// load lasted. Each timed-out call also left its goroutine (and Incus
// connection) behind, because the probe client had no timeout.
//
// localHealth fixes the three parts of that:
//
//   - the probe is Incus' own server-info call only, so its cost does not grow
//     with the number of tenants;
//   - one probe runs at a time and concurrent callers share its result, so a
//     burst of placements on a loaded host adds one request, not N, and a wedged
//     incusd holds one goroutine, not one per attempt;
//   - a failed or slow sample does not flip the verdict while Incus has proved
//     itself alive within the grace window; only failures that persist past it
//     (or a host that has never answered) report unhealthy.
//
// It still fails closed: a daemon that really is wedged stops being placeable
// once the grace window has passed.
const (
	// defaultLocalHealthTimeout is how long a caller waits for the probe.
	defaultLocalHealthTimeout = 3 * time.Second
	// minLocalHealthTimeout / maxLocalHealthTimeout bound the operator override
	// (CONTAINARIUM_LOCAL_HEALTH_TIMEOUT) so a typo cannot disable the check or
	// stall every placement for minutes.
	minLocalHealthTimeout = 100 * time.Millisecond
	maxLocalHealthTimeout = 30 * time.Second
	// localHealthGrace is how long one successful probe keeps the verdict
	// healthy while later probes fail or run slow.
	localHealthGrace = 15 * time.Second
	// probeCeilingFactor lets a slow probe finish (and prove Incus alive)
	// after the caller gave up, bounded so the goroutine cannot live forever.
	probeCeilingFactor = 3
	// slowProbeFraction: a successful probe that used more than 1/N of the
	// budget is logged, so saturation is visible before it flips the verdict.
	slowProbeFraction = 4
	slowLogInterval   = 30 * time.Second
