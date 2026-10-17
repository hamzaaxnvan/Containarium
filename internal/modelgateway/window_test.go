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
	}
}

func TestRecentSet_PrunesExpiredFromLenAndValues(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := newRecentSet(time.Minute)
	s.observe(base, "a")
	s.observe(base, "b")
	s.observe(base.Add(90*time.Second), "c")

	now := base.Add(2 * time.Minute)
	if n := s.len(now); n != 1 {
		t.Errorf("len = %d, want 1 (a and b expired)", n)
	}
	if v := s.values(now); len(v) != 1 || v[0] != "c" {
		t.Errorf("values = %v, want [c]", v)
	}
}

func TestEWMA_FirstSampleSeedsThenWeights(t *testing.T) {
	e := newEWMA(0.5)
	if v, n := e.get(); v != 0 || n != 0 {
		t.Errorf("zero value = %v/%d, want 0/0", v, n)
	}

	e.observe(100)
	if v, n := e.get(); v != 100 || n != 1 {
		t.Errorf("after first sample = %v/%d, want 100/1 (seed, not decay toward zero)", v, n)
	}

	e.observe(200)
	if v, _ := e.get(); v != 150 {
		t.Errorf("after second sample = %v, want 150", v)
	}
}

func TestSourceNet(t *testing.T) {
	cases := []struct{ in, want string }{
		{"10.0.0.5:1234", "10.0.0.0/24"},
		{"10.0.0.200:9", "10.0.0.0/24"},
		{"203.0.113.7:80", "203.0.113.0/24"},
		{"10.0.0.5", "10.0.0.0/24"}, // no port
		{"[2001:db8:1:2::1]:443", "2001:db8:1::/48"},
		{"", ""},
		{"not-an-ip:80", ""},
	}
	for _, c := range cases {
		if got := sourceNet(c.in); got != c.want {
			t.Errorf("sourceNet(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The endpoint and model sets are keyed on caller-controlled input, so an
// unbounded map would let one box grow a tenant's state without limit for the
// whole novelty TTL and make every observation's scan more expensive.
func TestRecentSet_BoundedAndEvictsOldest(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := newRecentSet(24 * time.Hour)

	now := base
	for i := 0; i < maxRecentValues*20; i++ {
		now = now.Add(time.Second)
		s.observe(now, "/v1/"+strconv.Itoa(i))
	}
	if n := s.len(now); n > maxRecentValues {
		t.Errorf("set grew to %d, want <= %d", n, maxRecentValues)
	}

	// The most recent values survive; the oldest were evicted.
	vals := s.values(now)
	found := map[string]bool{}
	for _, v := range vals {
		found[v] = true
	}
	if !found["/v1/"+strconv.Itoa(maxRecentValues*20-1)] {
		t.Error("most recent value was evicted")
	}
	if found["/v1/0"] {
		t.Error("oldest value survived eviction")
	}
}
