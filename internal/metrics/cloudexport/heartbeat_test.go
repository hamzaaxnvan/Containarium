package cloudexport

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

// heartbeatOf returns the single heartbeat datapoint from a flattened
// collection, or fails if it is absent or duplicated. Isolating the
// lookup keeps the heartbeat tests focused on the one series they assert.
func heartbeatOf(t *testing.T, pts []point) point {
	t.Helper()
	var found []point
	for _, p := range pts {
		if p.name == MetricHeartbeat {
			found = append(found, p)
		}
	}
	if len(found) == 0 {
		t.Fatalf("heartbeat series %q not emitted", MetricHeartbeat)
	}
	if len(found) > 1 {
		t.Fatalf("heartbeat series %q emitted %d times, want exactly 1", MetricHeartbeat, len(found))
	}
	return found[0]
}

// TestHeartbeatEmittedEveryInterval covers acceptance criterion #1: a
// heartbeat/up series is emitted on every export interval while the daemon
// runs. The ManualReader stands in for the PeriodicReader; each Collect is
// one interval tick. The heartbeat must be present, integer-typed, and
// exactly 1 on every tick.
