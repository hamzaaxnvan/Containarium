package sentinel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// #1358: on 2026-08-14 09:17 UTC the primary backend was lost (host reboot,
// ~2.5 min) and the sentinel failed over to a tunnel peer in under a second.
// Across that entire real event:
//
//	sentinel_preempted_total 0
//	sentinel_state           1
//
// Neither moved, because `preempted_total` is incremented from the GCP
// event-watcher path (handleEvent/EventPreempted) and `state` only leaves 1
// when the sentinel enters maintenance — which a successful failover avoids.
// So a multi-backend sentinel could lose backends all day and every existing
// series would sit still. These tests pin the two series that make that
// visible.

func TestRenderBackendMetrics_FailoverCounterAndPerBackendHealth(t *testing.T) {
	out := renderBackendMetrics(7, map[string]bool{
		"gcp":               false,
		"tunnel-fts-13700k": true,
		"tunnel-byoc-1":     true,
	})

	for _, want := range []string{
		"# TYPE sentinel_failover_total counter",
		"sentinel_failover_total 7",
		"# TYPE sentinel_backend_healthy gauge",
		`sentinel_backend_healthy{backend="gcp"} 0`,
		`sentinel_backend_healthy{backend="tunnel-byoc-1"} 1`,
		`sentinel_backend_healthy{backend="tunnel-fts-13700k"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, out)
		}
	}

	// HELP/TYPE are per-family, not per-sample; repeating them for each
	// labelled series is malformed exposition.
	if n := strings.Count(out, "# TYPE sentinel_backend_healthy"); n != 1 {
		t.Errorf("emitted %d TYPE lines for sentinel_backend_healthy, want exactly 1\n--- got ---\n%s", n, out)
	}
}

// Deterministic ordering keeps a scrape diff readable and keeps these tests
