package server

// #2317: the local backend's liveness verdict must not depend on how many
// instances the host runs, must not flip on one slow sample, and must not
// pile up a goroutine per placement while incusd is wedged.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}
func (l *logSink) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

func newTestHealth(probe func(ctx context.Context) error, timeout, grace time.Duration) (*localHealth, *fakeClock, *logSink) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	sink := &logSink{}
	h := newLocalHealth(probe, timeout, grace)
	h.now = clk.now
	h.logf = sink.logf
	return h, clk, sink
}

func TestLocalHealth_FastSuccess_IsHealthy(t *testing.T) {
	h, _, _ := newTestHealth(func(context.Context) error { return nil }, 200*time.Millisecond, time.Minute)
	if !h.Healthy() {
		t.Fatal("a probe that answers must report healthy")
	}
}

// Fail closed: with no prior success there is nothing to be lenient about.
func TestLocalHealth_ErrorWithNoPriorSuccess_IsUnhealthy(t *testing.T) {
	h, _, _ := newTestHealth(func(context.Context) error { return errors.New("connection refused") }, 200*time.Millisecond, time.Minute)
	if h.Healthy() {
		t.Fatal("a failing probe with no prior success must fail closed")
	}
}

func TestLocalHealth_SlowProbeWithNoPriorSuccess_IsUnhealthy(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h, _, _ := newTestHealth(func(context.Context) error { <-release; return nil }, 40*time.Millisecond, time.Minute)
	start := time.Now()
	if h.Healthy() {
		t.Fatal("a probe slower than the budget, with no prior success, must be unhealthy")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("Healthy() blocked %v; it must return within about the probe budget", took)
	}
}

// One slow or failed sample right after a success must not flip placement.
func TestLocalHealth_FailureWithinGrace_StaysHealthy_ThenFlipsAfterGrace(t *testing.T) {
	var fail atomic.Bool
	h, clk, sink := newTestHealth(func(context.Context) error {
		if fail.Load() {
			return errors.New("timeout")
		}
		return nil
	}, 200*time.Millisecond, 15*time.Second)

	if !h.Healthy() {
		t.Fatal("setup: first probe should be healthy")
	}
	fail.Store(true)
	clk.advance(5 * time.Second)
	if !h.Healthy() {
		t.Fatal("a failed sample 5s after a success (grace 15s) must keep the host healthy")
	}
	clk.advance(11 * time.Second) // 16s since the last success
	if h.Healthy() {
		t.Fatal("failures persisting past the grace window must flip the verdict to unhealthy")
	}
	if got := sink.count("local backend unhealthy"); got != 1 {
		t.Errorf("transition to unhealthy logged %d times, want exactly 1", got)
	}
}

// A late answer (after the caller already gave up) still proves incusd is alive.
func TestLocalHealth_LateSuccessRefreshesGrace(t *testing.T) {
	var slow atomic.Bool
	release := make(chan struct{})
	h, clk, _ := newTestHealth(func(context.Context) error {
		if slow.Load() {
			<-release
		}
		return nil
	}, 40*time.Millisecond, 15*time.Second)

	slow.Store(true)
	if h.Healthy() {
		t.Fatal("setup: a probe slower than the budget with no history is unhealthy")
	}
	close(release) // incusd answers, late
	waitUntil(t, func() bool { h.mu.Lock(); defer h.mu.Unlock(); return !h.lastOK.IsZero() })
	slow.Store(false)
	clk.advance(2 * time.Second)
	// next probe fails, but the late success was 2s ago, inside the grace window
	h.probe = func(context.Context) error { return errors.New("timeout") }
	if !h.Healthy() {
		t.Fatal("the late success must count as a recent proof of life")
	}
}

// Concurrent placements must share one probe, not stack N of them on a loaded host.
func TestLocalHealth_ConcurrentCallers_ShareOneProbe(t *testing.T) {
	var calls atomic.Int32
	gate := make(chan struct{})
	h, _, _ := newTestHealth(func(context.Context) error {
		calls.Add(1)
		<-gate
		return nil
	}, 2*time.Second, time.Minute)

	var wg sync.WaitGroup
	results := make([]bool, 20)
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i] = h.Healthy() }(i)
	}
	waitUntil(t, func() bool { return calls.Load() >= 1 })
	time.Sleep(30 * time.Millisecond) // let the other callers join the in-flight probe
	close(gate)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe ran %d times for 20 concurrent callers, want 1 (single flight)", got)
