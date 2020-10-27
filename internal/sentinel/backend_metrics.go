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
