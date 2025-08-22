package autosleep

import (
	"bytes"
	"context"
	"errors"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// --- fakes ---

type fakeIncus struct {
	containers []incus.ContainerInfo
	err        error
}

func (f *fakeIncus) ListContainers() ([]incus.ContainerInfo, error) {
	return f.containers, f.err
}

type fakeTraffic struct {
	per map[string]time.Time
	err error
}

func (f *fakeTraffic) LastNetworkActivity(_ context.Context, name string) (time.Time, error) {
	if f.err != nil {
		return time.Time{}, f.err
	}
	return f.per[name], nil
}

type stopperCall struct {
	username    string
	reason      string
	idleMinutes int
}

type fakeStopper struct {
	mu    sync.Mutex
	calls []stopperCall
	// errs, if non-nil, returns one error per call by index. A short
	// slice means later calls fall back to nil — same semantics as
	// "first call fails, rest succeed".
	errs []error
}

func (f *fakeStopper) StopForAutoSleep(_ context.Context, username, reason string, idleMinutes int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := len(f.calls)
	f.calls = append(f.calls, stopperCall{username: username, reason: reason, idleMinutes: idleMinutes})
	if idx < len(f.errs) {
		return f.errs[idx]
	}
	return nil
}

func (f *fakeStopper) recorded() []stopperCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]stopperCall, len(f.calls))
	copy(out, f.calls)
	return out
}

type auditCall struct {
	event  string
	fields map[string]any
}

type fakeAudit struct {
	mu    sync.Mutex
	calls []auditCall
}

func (f *fakeAudit) Log(event string, fields map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, auditCall{event: event, fields: fields})
}

func (f *fakeAudit) recorded() []auditCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]auditCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// --- tests ---

// TestManager_TickStopsIdleContainersAndAudits is the happy path:
// three containers in one tick — one idle user container sleeps, one
// recently-active user container is left alone, one core container is
// always left alone even with autosleep on (defense in depth — we
// shouldn't see core containers with the flag in practice).
func TestManager_TickStopsIdleContainersAndAudits(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	inc := &fakeIncus{
		containers: []incus.ContainerInfo{
			{
				Name:                 "alice-container",
				State:                "Running",
				AutoSleepEnabled:     true,
