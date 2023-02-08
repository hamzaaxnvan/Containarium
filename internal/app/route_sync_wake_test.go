package app

import (
	"testing"
)

// fakeWakeTracker satisfies WakeTracker for tests. Returns whatever
// the configured `entries` map says about a container name.
type fakeWakeTracker struct {
	entries map[string]wakeEntryTuple
}

type wakeEntryTuple struct {
	host string
	port int
}

func (f *fakeWakeTracker) IsInWakeMode(name string) (string, int, bool) {
	if f == nil {
		return "", 0, false
	}
	e, ok := f.entries[name]
	if !ok {
		return "", 0, false
	}
	return e.host, e.port, true
}

// TestEffectiveUpstream_TrackerHit — tracker reports wake mode → the
// returned upstream is the wake host/port, not the route's
// TargetIP/TargetPort.
func TestEffectiveUpstream_TrackerHit(t *testing.T) {
	job := &RouteSyncJob{}
	job.SetWakeTracker(&fakeWakeTracker{
		entries: map[string]wakeEntryTuple{
			"alice-container": {host: "10.0.3.1", port: 8080},
		},
	})
	route := &RouteRecord{
		ContainerName: "alice-container",
		TargetIP:      "10.0.0.42",
		TargetPort:    9000,
	}
	ip, port := job.effectiveUpstream(route)
	if ip != "10.0.3.1" || port != 8080 {
		t.Errorf("effectiveUpstream = (%s,%d), want (10.0.3.1,8080)", ip, port)
	}
}

// TestEffectiveUpstream_TrackerMiss — tracker says not-in-wake-mode →
// returns the route's direct TargetIP / TargetPort.
func TestEffectiveUpstream_TrackerMiss(t *testing.T) {
	job := &RouteSyncJob{}
	job.SetWakeTracker(&fakeWakeTracker{entries: map[string]wakeEntryTuple{}})
	route := &RouteRecord{
		ContainerName: "alice-container",
		TargetIP:      "10.0.0.42",
		TargetPort:    9000,
	}
	ip, port := job.effectiveUpstream(route)
	if ip != "10.0.0.42" || port != 9000 {
		t.Errorf("effectiveUpstream = (%s,%d), want direct (10.0.0.42,9000)", ip, port)
	}
}

// TestEffectiveUpstream_NilTracker — backward compat: a job without
// SetWakeTracker behaves exactly as it did before Phase 3.
func TestEffectiveUpstream_NilTracker(t *testing.T) {
	job := &RouteSyncJob{}
	route := &RouteRecord{
		ContainerName: "alice-container",
		TargetIP:      "10.0.0.42",
		TargetPort:    9000,
	}
	ip, port := job.effectiveUpstream(route)
	if ip != "10.0.0.42" || port != 9000 {
		t.Errorf("nil tracker effectiveUpstream = (%s,%d), want (10.0.0.42,9000)", ip, port)
	}
}

// TestEffectiveUpstream_EmptyContainerName — defensive: a route record
// with no container name (legacy or hand-rolled rows) must not consult
// the tracker (it would falsely match the empty key).
func TestEffectiveUpstream_EmptyContainerName(t *testing.T) {
	job := &RouteSyncJob{}
	job.SetWakeTracker(&fakeWakeTracker{
		entries: map[string]wakeEntryTuple{
			"": {host: "10.0.3.1", port: 8080}, // shouldn't be consulted
		},
	})
	route := &RouteRecord{
		ContainerName: "",
		TargetIP:      "10.0.0.42",
		TargetPort:    9000,
	}
	ip, port := job.effectiveUpstream(route)
	if ip != "10.0.0.42" || port != 9000 {
