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
	var buf bytes.Buffer
	prevOut := log.Writer()
	defer log.SetOutput(prevOut)
	log.SetOutput(&buf)

	m := NewManager(inc, del, Options{
		Interval: time.Hour,
		Clock:    func() time.Time { return now },
	})
	m.tick(context.Background())

	calls := del.recorded()
	if len(calls) != 2 {
		t.Fatalf("both containers should be attempted, got %d: %+v", len(calls), calls)
	}
	if !bytes.Contains(buf.Bytes(), []byte("delete boom")) {
		t.Errorf("delete error not logged; output:\n%s", buf.String())
	}
}

// TestManager_StopIsIdempotent — Stop called twice must not panic
// and must complete quickly both times. Uses a short interval so the
// ticker is actively firing during the Stop window.
func TestManager_StopIsIdempotent(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	m := NewManager(&fakeIncus{}, &fakeDeleter{}, Options{
		Interval: 25 * time.Millisecond,
		Clock:    func() time.Time { return now },
	})
	m.Start(context.Background())
	time.Sleep(75 * time.Millisecond)

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		m.Stop()
	}()
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first Stop() blocked >2s")
	}

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		m.Stop()
	}()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second Stop() blocked >1s; expected no-op")
	}
}

// TestManager_ContextCancellationStopsTicker — Start(ctx) with a
// canceled ctx exits the run loop within a tick interval.
func TestManager_ContextCancellationStopsTicker(t *testing.T) {
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	m := NewManager(&fakeIncus{}, &fakeDeleter{}, Options{
		Interval: 25 * time.Millisecond,
	})
	m.Start(ctx)
	time.Sleep(75 * time.Millisecond) // let several ticks fire

	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before+1 {
		t.Errorf("goroutine leak: before=%d after=%d", before, got)
	}
	_ = m
}

// TestManager_StartIsNonBlocking — Start spawns the loop and returns
// immediately. A blocked Start would hang the daemon's startup.
func TestManager_StartIsNonBlocking(t *testing.T) {
	var ticks int64
	inc := &fakeIncusCounting{count: &ticks}
	m := NewManager(inc, &fakeDeleter{}, Options{
