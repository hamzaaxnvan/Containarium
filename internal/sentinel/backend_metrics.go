package sentinel

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// Backend-failover observability (#1358).
//
// On 2026-08-14 09:17 UTC the primary backend went away (host reboot, ~2.5
// min) and the sentinel failed over to a tunnel peer in under a second — the
// multi-backend design working exactly as intended. But across that entire
// real event the exported series read:
//
//	sentinel_preempted_total 0
//	sentinel_state           1
//
// Neither moved. `sentinel_preempted_total` is incremented from the GCP
// event-watcher path (handleEvent/EventPreempted), so a loss detected by the
// health checker never touches it; `sentinel_state` only leaves 1 when the
// sentinel enters maintenance, which a successful failover is precisely what
// avoids. A sentinel fronting four backends could lose them one after another
// and every series on /metrics would sit perfectly still.
//
// That also makes those two series the wrong marker for the #1349 capture
// window: the allocation blowup is tied to losing a backend, and this proves a
// backend can be lost without either series noticing.
//
// So two additions:
//
//   - sentinel_failover_total — a counter that moves on every primary switch,
//     whether or not maintenance was ever entered.
//   - sentinel_backend_healthy{backend="..."} — per-backend health, so "gcp
//     went away but we stayed up on a tunnel" is visible and alertable rather
//     than living only in the journal.
//
// sentinel_preempted_total is deliberately left alone. It answers a different
// and still-useful question — "we had nowhere to fail over to" — and conflating
// the two would lose that.

// recordFailover counts one primary-backend switch. Called from the health
// check loop; read from the /metrics HTTP goroutine, hence the atomic.
func (m *Manager) recordFailover() {
	m.failoverTotal.Add(1)
}

// recordFailback counts one switch back to a preferred primary (#2476).
func (m *Manager) recordFailback() {
	m.failbackTotal.Add(1)
}

// FailbackCount returns the number of failbacks to a preferred primary.
func (m *Manager) FailbackCount() int64 {
	return m.failbackTotal.Load()
}

// primaryIsPreferred reports whether the current primary is the best-priority
// backend registered (healthy or not). False means the sentinel is sitting on a
// fallback, which is what an alert should page on if it lasts more than a few
// minutes: it is the condition #2476 left in place indefinitely.
func (m *Manager) primaryIsPreferred() bool {
	m.mu.RLock()
	cur := m.primary
	m.mu.RUnlock()
	if cur == nil {
		return true // maintenance / nothing elected: not "on a fallback"
	}
	for _, b := range m.backends.All() {
		if b.Priority < cur.Priority {
			return false
		}
	}
	return true
}

// renderFailbackMetrics is the exposition for the failback counter and the
// preferred-primary gauge. Pure, like renderBackendMetrics.
func renderFailbackMetrics(failbacks int64, preferred bool) string {
	var b bytes.Buffer
	fmt.Fprint(&b, "# HELP sentinel_failback_total Total switches back to a preferred primary after it recovered.\n")
	fmt.Fprint(&b, "# TYPE sentinel_failback_total counter\n")
	fmt.Fprintf(&b, "sentinel_failback_total %d\n", failbacks)
	v := 0
	if preferred {
		v = 1
	}
	fmt.Fprint(&b, "# HELP sentinel_primary_preferred 1 if the active primary is the best-priority registered backend, 0 if the sentinel is on a fallback.\n")
	fmt.Fprint(&b, "# TYPE sentinel_primary_preferred gauge\n")
	fmt.Fprintf(&b, "sentinel_primary_preferred %d\n", v)
	return b.String()
