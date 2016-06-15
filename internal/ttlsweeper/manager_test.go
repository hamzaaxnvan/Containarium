package ttlsweeper

import (
	"bytes"
	"context"
	"errors"
	"log"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- fakes ---

type fakeIncus struct {
	containers []ContainerView
	err        error
}

func (f *fakeIncus) ListContainers() ([]ContainerView, error) {
	return f.containers, f.err
}

type deleteCall struct {
	name   string
	reason string
}

type fakeDeleter struct {
	mu    sync.Mutex
	calls []deleteCall
	// errs returns one error per call by index. A short slice means
	// later calls fall back to nil (same semantics as autosleep's
	// fakeStopper).
	errs []error
}

func (f *fakeDeleter) DeleteContainer(_ context.Context, name, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := len(f.calls)
	f.calls = append(f.calls, deleteCall{name: name, reason: reason})
	if idx < len(f.errs) {
		return f.errs[idx]
	}
	return nil
}

func (f *fakeDeleter) recorded() []deleteCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]deleteCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// names returns the recorded delete-call names sorted, for
// order-independent assertions.
func (f *fakeDeleter) names() []string {
	calls := f.recorded()
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.name
	}
	sort.Strings(out)
	return out
}

// --- tests ---

// TestManager_TickDeletesExpiredOnly is the happy path: three
// containers — one expired (with TTL in the past), one active (TTL in
// the future), one with no TTL set. Only the expired one is deleted,
// and the deletion reason includes the expiry timestamp.
func TestManager_TickDeletesExpiredOnly(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	expiredAt := now.Add(-time.Hour)
	futureAt := now.Add(time.Hour)

	inc := &fakeIncus{containers: []ContainerView{
		{Name: "alice-container", TTLExpiresAt: &expiredAt},
		{Name: "bob-container", TTLExpiresAt: &futureAt},
		{Name: "carol-container", TTLExpiresAt: nil},
	}}
	del := &fakeDeleter{}
	m := NewManager(inc, del, Options{
		Interval: time.Hour, // never fires; tick called directly.
		Clock:    func() time.Time { return now },
	})
	m.tick(context.Background())

	calls := del.recorded()
	if len(calls) != 1 {
		t.Fatalf("expected 1 delete call, got %d: %+v", len(calls), calls)
	}
	if calls[0].name != "alice-container" {
		t.Errorf("deleted name = %q, want alice-container", calls[0].name)
	}
	if !bytes.Contains([]byte(calls[0].reason), []byte("ttl_expired")) {
		t.Errorf("reason = %q, want substring ttl_expired", calls[0].reason)
	}
	if !bytes.Contains([]byte(calls[0].reason), []byte("2026-05-23T11:00:00Z")) {
		t.Errorf("reason = %q, want expiry timestamp embedded", calls[0].reason)
	}
}

// TestManager_TickIsNoOpWhenNothingExpired locks in that the deleter
// is not called at all in the common case (no expired containers).
// The Manager should not be chatty when there's nothing to do.
func TestManager_TickIsNoOpWhenNothingExpired(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	futureAt := now.Add(time.Hour)
	inc := &fakeIncus{containers: []ContainerView{
		{Name: "alice-container", TTLExpiresAt: &futureAt},
		{Name: "bob-container", TTLExpiresAt: nil},
	}}
	del := &fakeDeleter{}
	m := NewManager(inc, del, Options{
		Interval: time.Hour,
		Clock:    func() time.Time { return now },
	})
	m.tick(context.Background())
	if calls := del.recorded(); len(calls) != 0 {
		t.Fatalf("expected 0 delete calls, got %d: %+v", len(calls), calls)
	}
}

// TestManager_IncusListError_LogsAndContinues — ListContainers
// failure on tick #1 must not crash; tick #2 with a working list
// proceeds normally. Matches autosleep's resilience model.
func TestManager_IncusListError_LogsAndContinues(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)

	inc := &fakeIncus{err: errors.New("incus offline")}
	del := &fakeDeleter{}
	m := NewManager(inc, del, Options{
		Interval: time.Hour,
		Clock:    func() time.Time { return now },
	})
	m.tick(context.Background())
	if calls := del.recorded(); len(calls) != 0 {
		t.Fatalf("error tick must not call Delete, got %+v", calls)
	}

	// Recovery: next tick lists successfully and deletes.
	inc.err = nil
	expiredAt := now.Add(-time.Hour)
	inc.containers = []ContainerView{
		{Name: "alice-container", TTLExpiresAt: &expiredAt},
	}
	m.tick(context.Background())
	if calls := del.recorded(); len(calls) != 1 {
		t.Fatalf("post-recovery tick should delete once, got %d", len(calls))
	}
}

// TestManager_DeleterError_ContinuesWithOthers — when the first delete
// fails, the second eligible container still gets deleted. One
// per-container failure must not poison the rest of the tick.
func TestManager_DeleterError_ContinuesWithOthers(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	expiredAt := now.Add(-time.Hour)
	inc := &fakeIncus{containers: []ContainerView{
		{Name: "alice-container", TTLExpiresAt: &expiredAt},
		{Name: "bob-container", TTLExpiresAt: &expiredAt},
	}}
	del := &fakeDeleter{errs: []error{errors.New("delete boom")}}

	// Capture log output to verify the error is logged.
