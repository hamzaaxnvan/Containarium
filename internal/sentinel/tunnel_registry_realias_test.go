package sentinel

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// #1139: after a backend is down for a long stretch and returns on the same
// internal IP, the sentinel could keep stale upstream forwarding state until
// an operator restarted the service by hand.
//
// One concrete gap in that class: the reconnect path reused the recorded
// loopback IP without re-asserting it on `lo`. Reusing an address is not the
// same as the address still existing — the registry's map outlives the alias
// across a host network reconfiguration or an `ip addr flush` — and nothing
// else re-adds it, so forwarding points at an address the kernel no longer
// has.

// aliasRecorder counts alias adds so a test can see whether reconnect
// re-asserted one.
type aliasRecorder struct {
	added   []string
	removed []string
	err     error
}

func (a *aliasRecorder) add(ip string) error {
	if a.err != nil {
		return a.err
	}
	a.added = append(a.added, ip)
	return nil
}

func (a *aliasRecorder) remove(ip string) {
	a.removed = append(a.removed, ip)
}

// withRecordedAliases swaps in the recorder for the duration of a test.
func withRecordedAliases(t *testing.T) *aliasRecorder {
	t.Helper()
	rec := &aliasRecorder{}
	origAdd, origRemove := addLoopbackAliasFn.Load(), removeLoopbackAliasFn.Load()
	add := func(ip string) error { return rec.add(ip) }
	remove := func(ip string) { rec.remove(ip) }
	addLoopbackAliasFn.Store(&add)
	removeLoopbackAliasFn.Store(&remove)
	t.Cleanup(func() {
		addLoopbackAliasFn.Store(origAdd)
		removeLoopbackAliasFn.Store(origRemove)
	})
	return rec
}

func TestRegister_ReassertsLoopbackAliasOnReconnect(t *testing.T) {
	rec := withRecordedAliases(t)
	r := NewTunnelRegistry()

	ip1, _, err := r.Register(&TunnelHandshake{SpotID: "spot-a"}, nil, TunnelTransportTLS)
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if len(rec.added) != 1 {
		t.Fatalf("first registration added %d aliases, want 1", len(rec.added))
	}

