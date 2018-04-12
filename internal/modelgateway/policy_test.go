package modelgateway

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// fakeClock drives the ladder deterministically. Every timing property here —
// cooldown, circuit-break expiry, throttle spacing, window ageing — is a
// duration, and testing those against the wall clock means either sleeping or
// flaking. So the policy takes its clock from config and the tests own it.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// recordingSink captures policy transitions.
type recordingSink struct {
	mu     sync.Mutex
	events []PolicyEvent
}

func (s *recordingSink) PolicyTransition(ev PolicyEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *recordingSink) all() []PolicyEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]PolicyEvent(nil), s.events...)
}

func (s *recordingSink) transitionsTo(st PolicyState) int {
	n := 0
	for _, e := range s.all() {
		if e.To == st {
			n++
		}
	}
	return n
}

func req(tenant string) RequestInfo {
	return RequestInfo{Tenant: tenant, Provider: "anthropic", Model: "m1", Endpoint: "/v1/messages", RemoteAddr: "10.0.0.5:1234"}
}

// A gateway upgraded without any policy configuration must behave exactly as it
// did before: no quota, no detection, nothing denied. This is the property that
// makes the feature safe to ship, so it is the first test.
func TestPolicy_ZeroConfigIsInert(t *testing.T) {
	p := NewPolicy(PolicyConfig{})
	for i := 0; i < 500; i++ {
		p.RecordUsage("acme", Usage{InputTokens: 10_000, OutputTokens: 10_000})
		d := p.Check(req("acme"))
		if !d.Allow {
			t.Fatalf("call %d denied under zero config: %+v", i, d)
		}
		if d.State != StateObserve {
			t.Fatalf("call %d state = %s, want observe", i, d.State)
		}
	}
}

func TestPolicy_QuotaExceeded_CircuitBreaks(t *testing.T) {
	clk := newFakeClock()
	sink := &recordingSink{}
	p := NewPolicy(PolicyConfig{
		Quota:  QuotaLimits{Window: time.Minute, MaxTotalTokens: 100},
		Now:    clk.now,
		Alerts: sink,
	})

	if d := p.Check(req("acme")); !d.Allow {
		t.Fatalf("first call denied: %+v", d)
	}
	p.RecordUsage("acme", Usage{InputTokens: 80, OutputTokens: 40}) // 120 > 100

	d := p.Check(req("acme"))
	if d.Allow {
		t.Fatal("call admitted after the token budget was exhausted")
	}
	if d.State != StateCircuitBreak {
		t.Errorf("state = %s, want circuit_break", d.State)
	}
	if d.Status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", d.Status)
	}
	if d.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want a positive hint", d.RetryAfter)
	}
	if sink.transitionsTo(StateCircuitBreak) != 1 {
		t.Errorf("want exactly one circuit-break transition, got %d: %+v",
			sink.transitionsTo(StateCircuitBreak), sink.all())
	}
}

// The budget is a rolling window, not a lifetime cap: once the spend ages out
// the tenant works again with no operator action.
func TestPolicy_QuotaRecoversAsWindowSlides(t *testing.T) {
	clk := newFakeClock()
	p := NewPolicy(PolicyConfig{
		Quota:           QuotaLimits{Window: time.Minute, MaxTotalTokens: 100},
		CircuitBreakFor: 10 * time.Second,
		Cooldown:        10 * time.Second,
		Now:             clk.now,
	})

	p.RecordUsage("acme", Usage{InputTokens: 200})
	if d := p.Check(req("acme")); d.Allow {
		t.Fatal("expected denial while over budget")
	}

	// Past the window: the spend has aged out entirely.
	clk.advance(2 * time.Minute)

	// De-escalation is one rung per cooldown, so drive several evaluations.
	var last Decision
	for i := 0; i < 6; i++ {
		last = p.Check(req("acme"))
		clk.advance(11 * time.Second)
	}
	if !last.Allow {
		t.Fatalf("tenant never recovered after the window slid: %+v", last)
	}
	if last.State != StateObserve {
		t.Errorf("final state = %s, want observe", last.State)
	}
}

func TestPolicy_ApproachingQuota_ThrottlesWithSpacing(t *testing.T) {
	clk := newFakeClock()
	p := NewPolicy(PolicyConfig{
		Quota:               QuotaLimits{Window: time.Minute, MaxTotalTokens: 100},
		ThrottleAtQuota:     0.8,
		ThrottleMinInterval: 2 * time.Second,
		Now:                 clk.now,
	})

	p.RecordUsage("acme", Usage{InputTokens: 85}) // 85% — over the throttle mark, under the cap

	d1 := p.Check(req("acme"))
	if !d1.Allow {
		t.Fatalf("first throttled call should still be admitted: %+v", d1)
	}
	if d1.State != StateThrottle {
		t.Errorf("state = %s, want throttle", d1.State)
	}

	// Immediately again: inside the minimum spacing.
	d2 := p.Check(req("acme"))
	if d2.Allow {
		t.Fatal("second call admitted inside the throttle interval")
	}
	if d2.Status != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", d2.Status)
	}
	if d2.RetryAfter <= 0 || d2.RetryAfter > 2*time.Second {
		t.Errorf("RetryAfter = %v, want (0,2s]", d2.RetryAfter)
	}

	// After the interval it is admitted again — throttled, not blocked.
	clk.advance(2 * time.Second)
	if d3 := p.Check(req("acme")); !d3.Allow {
		t.Fatalf("call denied after the throttle interval elapsed: %+v", d3)
	}
}

func TestPolicy_CircuitBreakStepsDownWhenConditionClears(t *testing.T) {
	clk := newFakeClock()
	p := NewPolicy(PolicyConfig{
		Quota:           QuotaLimits{Window: time.Minute, MaxTotalTokens: 100},
		CircuitBreakFor: 30 * time.Second,
		Now:             clk.now,
	})

	p.RecordUsage("acme", Usage{InputTokens: 500})
	if d := p.Check(req("acme")); d.State != StateCircuitBreak {
		t.Fatalf("state = %s, want circuit_break", d.State)
	}

	// Still over budget when the break elapses: it extends rather than flapping.
	clk.advance(31 * time.Second)
	if d := p.Check(req("acme")); d.State != StateCircuitBreak {
		t.Errorf("state = %s while still over budget, want the break to hold", d.State)
	}

	// Spend ages out, then the break elapses: now it steps down.
	clk.advance(2 * time.Minute)
	if d := p.Check(req("acme")); d.State != StateAlert {
		t.Errorf("state = %s after the condition cleared, want alert (one rung down)", d.State)
	}
}

// Escalation is immediate but descent is damped and one rung at a time, so an
// incident that is still in progress can't be walked back by a quiet second.
func TestPolicy_DeEscalatesOneRungPerCooldown(t *testing.T) {
	clk := newFakeClock()
	p := NewPolicy(PolicyConfig{
		Quota:               QuotaLimits{Window: time.Minute, MaxTotalTokens: 100},
		ThrottleMinInterval: time.Nanosecond,
		CircuitBreakFor:     time.Second,
		Cooldown:            time.Minute,
		Now:                 clk.now,
	})

	p.RecordUsage("acme", Usage{InputTokens: 500})
	p.Check(req("acme")) // → circuit_break
	clk.advance(5 * time.Minute)

	got := []PolicyState{}
	for i := 0; i < 3; i++ {
		got = append(got, p.Check(req("acme")).State)
		clk.advance(61 * time.Second)
	}
	want := []PolicyState{StateAlert, StateThrottle, StateObserve}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("descent = %v, want %v", got, want)
		}
	}
}

func TestPolicy_ManualRevokeIsStickyUntilCleared(t *testing.T) {
	clk := newFakeClock()
	sink := &recordingSink{}
	p := NewPolicy(PolicyConfig{Cooldown: time.Second, Now: clk.now, Alerts: sink})

	p.Revoke("acme", "credential suspected stolen")

	// Perfectly clean traffic for a long time must not lift a revocation: it is
	// the one state that needs a human, which is the whole point of having it.
	for i := 0; i < 5; i++ {
		clk.advance(time.Hour)
		d := p.Check(req("acme"))
		if d.Allow {
			t.Fatal("revoked tenant admitted")
		}
		if d.State != StateRevoke {
			t.Fatalf("state = %s, want revoke", d.State)
		}
		if d.Status != http.StatusForbidden {
			t.Errorf("status = %d, want 403", d.Status)
		}
	}

	p.Clear("acme")
	if d := p.Check(req("acme")); !d.Allow || d.State != StateObserve {
		t.Fatalf("after Clear: %+v, want allowed/observe", d)
	}
	if sink.transitionsTo(StateRevoke) != 1 {
		t.Errorf("want 1 revoke transition, got %d", sink.transitionsTo(StateRevoke))
	}
}

// Anomaly detection must stay silent until it has actually learned the tenant,
// or every new tenant's first burst is an incident.
func TestPolicy_BaselineSilentUntilEnoughSamples(t *testing.T) {
	clk := newFakeClock()
	p := NewPolicy(PolicyConfig{
		Anomaly: AnomalyConfig{
			Enabled:            true,
			BaselineWindow:     time.Minute,
			BaselineMinSamples: 5,
			BaselineFactor:     4,
			NoveltyTTL:         time.Hour,
		},
		Now: clk.now,
	})

	// A huge first call, with no history to compare against.
	p.RecordUsage("acme", Usage{InputTokens: 5_000_000})
	d := p.Check(req("acme"))
	for _, s := range d.Signals {
		if s.Name == SignalRateBaseline {
			t.Fatalf("baseline signal fired with no established baseline: %+v", s)
		}
	}
}

func TestPolicy_RateSpikeAgainstLearnedBaseline(t *testing.T) {
	clk := newFakeClock()
	p := NewPolicy(PolicyConfig{
		Quota: QuotaLimits{Window: time.Minute},
		Anomaly: AnomalyConfig{
			Enabled:            true,
			BaselineWindow:     time.Minute,
			BaselineAlpha:      0.5,
			BaselineMinSamples: 3,
			BaselineFactor:     5,
			NoveltyTTL:         time.Hour,
		},
		AlertAt: 0.01, // any severity is enough to reach alert, for a crisp assertion
		BreakAt: 9,    // keep break out of the way
		Now:     clk.now,
	})

	// Teach a steady, modest rate: a few windows of light traffic.
	for i := 0; i < 6; i++ {
		p.RecordUsage("acme", Usage{InputTokens: 100})
		p.Check(req("acme"))
		clk.advance(70 * time.Second)
	}

	// Now a burst two orders of magnitude above that.
	for i := 0; i < 20; i++ {
		p.RecordUsage("acme", Usage{InputTokens: 50_000})
	}
	d := p.Check(req("acme"))

	var found *Signal
	for i := range d.Signals {
		if d.Signals[i].Name == SignalRateBaseline {
			found = &d.Signals[i]
		}
	}
