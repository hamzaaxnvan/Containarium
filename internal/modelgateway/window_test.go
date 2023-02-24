package modelgateway

import (
	"strconv"
	"testing"
	"time"
)

func TestRollingWindow_SumsRecentAndAgesOutOld(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := newRollingWindow(time.Minute)

	w.add(base, counts{calls: 1, inputTokens: 10})
	w.add(base.Add(10*time.Second), counts{calls: 1, inputTokens: 20})

	got := w.sum(base.Add(20 * time.Second))
	if got.calls != 2 || got.totalTokens() != 30 {
		t.Errorf("within window: calls=%d tokens=%d, want 2/30", got.calls, got.totalTokens())
	}

	// Past the window, everything has aged out — the property that makes a
	// quota a rolling budget rather than a lifetime cap.
	if got := w.sum(base.Add(2 * time.Minute)); got.calls != 0 || got.totalTokens() != 0 {
		t.Errorf("after the window: calls=%d tokens=%d, want 0/0", got.calls, got.totalTokens())
	}
}

// A ring bucket reused a full revolution later must be zeroed, not accumulated
// onto — otherwise usage from an hour ago silently counts against today.
func TestRollingWindow_ReusedBucketIsZeroed(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := newRollingWindow(time.Minute)

	w.add(base, counts{calls: 1, inputTokens: 1000})
	later := base.Add(time.Minute) // same ring slot, one revolution on
	w.add(later, counts{calls: 1, inputTokens: 5})

	got := w.sum(later)
	if got.totalTokens() != 5 {
		t.Errorf("tokens = %d, want 5 (stale bucket leaked)", got.totalTokens())
	}
}

// The rate must divide by the span actually observed, not the nominal window,
// or a burst in the first seconds of a window reads as a slow trickle and the
// baseline comparison never fires.
func TestRollingWindow_RateUsesObservedSpan(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := newRollingWindow(time.Minute) // slot = 5s

	w.add(base, counts{calls: 1, inputTokens: 1000})
	got := w.rate(base)

	// 1000 tokens in a single in-progress slot → floored at one slot (5s).
	if want := 200.0; got != want {
		t.Errorf("rate = %v, want %v (nominal-window division would give ~16.7)", got, want)
	}
	if empty := newRollingWindow(time.Minute).rate(base); empty != 0 {
		t.Errorf("empty window rate = %v, want 0", empty)
	}
}

func TestRecentSet_NoveltyThenExpiry(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := newRecentSet(time.Minute)

	if !s.observe(base, "claude") {
		t.Error("first sighting should report novel")
	}
	if s.observe(base.Add(time.Second), "claude") {
		t.Error("second sighting within the ttl should not report novel")
	}
	if !s.observe(base.Add(2*time.Minute), "claude") {
		t.Error("sighting after the ttl should report novel again")
	}
	// An empty value is not a value: it must neither be stored nor flagged.
	if s.observe(base, "") {
		t.Error("empty value reported as novel")
	}
	if n := s.len(base.Add(2 * time.Minute)); n != 1 {
		t.Errorf("len = %d, want 1", n)
