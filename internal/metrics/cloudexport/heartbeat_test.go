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
func TestHeartbeatEmittedEveryInterval(t *testing.T) {
	sources := &fakeSources{sr: sampleResources()}
	// Three consecutive ticks: the heartbeat is present with value 1 on
	// each, never drifting or dropping out.
	for tick := 1; tick <= 3; tick++ {
		rm := collectOnce(t, sources, sampleLabels())
		hb := heartbeatOf(t, flattenGauges(t, rm))
		if !hb.isInt {
			t.Fatalf("tick %d: heartbeat must be an int64 gauge, got float", tick)
		}
		if hb.ival != 1 {
			t.Fatalf("tick %d: heartbeat = %d, want 1", tick, hb.ival)
		}
	}
}

// TestHeartbeatSurvivesSourceError is the dead-man correctness guard: the
// heartbeat means "the daemon is alive and its export pipeline reaches the
// cloud," so a transient Sources error (e.g. incus briefly unavailable)
// must NOT suppress it — otherwise an incus hiccup masquerades as backend
// death and pages the operator. The host series are still skipped for that
// tick (no stale values); only the heartbeat survives.
func TestHeartbeatSurvivesSourceError(t *testing.T) {
	cases := map[string]*fakeSources{
		"sources error": {err: errors.New("incus unavailable")},
		"nil snapshot":  {sr: nil},
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			pts := flattenGauges(t, collectOnce(t, src, sampleLabels()))
			hb := heartbeatOf(t, pts)
			if hb.ival != 1 {
				t.Fatalf("heartbeat = %d, want 1 even when Sources fails", hb.ival)
			}
			// The heartbeat is the ONLY series on a failed tick: no host
