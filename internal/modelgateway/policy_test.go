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
	if found == nil {
		t.Fatalf("no baseline signal on a 100x spike; signals=%+v state=%s", d.Signals, d.State)
	}
	if found.Severity <= 0 {
		t.Errorf("severity = %v, want > 0", found.Severity)
	}
	if d.State < StateAlert {
		t.Errorf("state = %s, want at least alert", d.State)
	}
}

// A baseline that keeps learning during an incident eventually accepts the
// attack as normal. It must freeze while the tenant is off the observe rung.
func TestPolicy_BaselineFrozenWhileEscalated(t *testing.T) {
	clk := newFakeClock()
	p := NewPolicy(PolicyConfig{
		Quota: QuotaLimits{Window: time.Minute, MaxTotalTokens: 1000},
		Anomaly: AnomalyConfig{
			Enabled: true, BaselineWindow: time.Minute, BaselineMinSamples: 2, NoveltyTTL: time.Hour,
		},
		CircuitBreakFor: time.Hour, // hold the tenant off observe
		Now:             clk.now,
	})

	// Establish a baseline, then blow the budget so the tenant leaves observe.
	for i := 0; i < 3; i++ {
		p.RecordUsage("acme", Usage{InputTokens: 10})
		p.Check(req("acme"))
		clk.advance(61 * time.Second)
	}
	before := statusFor(t, p, "acme")

	p.RecordUsage("acme", Usage{InputTokens: 100_000})
	if d := p.Check(req("acme")); d.State == StateObserve {
		t.Fatal("expected the tenant to leave observe after exceeding budget")
	}
	for i := 0; i < 5; i++ {
		p.RecordUsage("acme", Usage{InputTokens: 100_000})
		clk.advance(61 * time.Second)
		p.Check(req("acme"))
	}

	after := statusFor(t, p, "acme")
	if after.Samples != before.Samples {
		t.Errorf("baseline kept learning while escalated: samples %d → %d", before.Samples, after.Samples)
	}
}

func TestPolicy_NoveltyAndConcurrencySignals(t *testing.T) {
	clk := newFakeClock()
	p := NewPolicy(PolicyConfig{
		Anomaly: AnomalyConfig{
			Enabled:              true,
			NoveltyTTL:           time.Hour,
			ConcurrentWindow:     time.Minute,
			ConcurrentSourceNets: 2,
			BaselineWindow:       time.Minute,
			BaselineMinSamples:   1,
			BaselineFactor:       1e9, // keep the rate signal out of this test
		},
		Now: clk.now,
	})

	base := RequestInfo{Tenant: "acme", Provider: "anthropic", Model: "haiku", Endpoint: "/v1/messages", RemoteAddr: "10.0.0.5:1"}

	// Cold start: while the tenant is still being learned, "first ever model" is
	// not a switch and must not fire. Priming past BaselineMinSamples is what
	// turns the novelty detectors on.
	if d := p.Check(base); hasSignal(d.Signals, SignalNewModel) || hasSignal(d.Signals, SignalNewEndpoint) {
		t.Fatalf("novelty fired on an unlearned tenant's first call: %+v", d.Signals)
	}
	clk.advance(61 * time.Second)
	p.Check(base) // folds the quiet window into the baseline

	// Established now, and the primed model/endpoint are no longer novel.
	d := p.Check(base)
	if hasSignal(d.Signals, SignalNewModel) || hasSignal(d.Signals, SignalNewEndpoint) {
		t.Errorf("repeat call flagged as novel: %+v", d.Signals)
	}

	// A model the tenant has never used — the "model-tier switch" shape.
	up := base
	up.Model = "opus"
	if d = p.Check(up); !hasSignal(d.Signals, SignalNewModel) {
		t.Errorf("switch to an unseen model not flagged: %+v", d.Signals)
	}

	// The same token arriving from more networks than a box population explains.
	for _, addr := range []string{"203.0.113.7:1", "198.51.100.9:1", "192.0.2.11:1"} {
		n := base
		n.RemoteAddr = addr
		d = p.Check(n)
	}
	if !hasSignal(d.Signals, SignalConcurrentNets) {
		t.Errorf("four distinct source networks not flagged: %+v", d.Signals)
	}
}

func TestPolicy_AuthFailureRatioSignal(t *testing.T) {
	clk := newFakeClock()
	p := NewPolicy(PolicyConfig{
		Quota: QuotaLimits{Window: time.Minute},
		Anomaly: AnomalyConfig{
			Enabled:            true,
			AuthFailureRatio:   0.5,
			AuthFailureMin:     4,
			BaselineMinSamples: 1000,
			NoveltyTTL:         time.Hour,
		},
		Now: clk.now,
	})

	p.RecordUsage("acme", Usage{InputTokens: 1})
	p.RecordUsage("acme", Usage{InputTokens: 1})
	for i := 0; i < 6; i++ {
		p.RecordDenial("acme") // probing: rejected provider/model
	}

	d := p.Check(req("acme"))
	if !hasSignal(d.Signals, SignalAuthFailureRate) {
		t.Fatalf("6 denials against 2 calls not flagged: %+v", d.Signals)
	}
}

// Automatic revocation is destructive and needs a human to undo, so it must not
// happen unless the operator opted in by setting RevokeAt.
func TestPolicy_NoAutoRevokeUnlessConfigured(t *testing.T) {
	clk := newFakeClock()
	mk := func(revokeAt float64) *Policy {
		return NewPolicy(PolicyConfig{
			Anomaly: AnomalyConfig{
				Enabled: true, NoveltyTTL: time.Hour,
				ConcurrentSourceNets: 1, ConcurrentWindow: time.Minute,
				BaselineMinSamples: 1000,
			},
			AlertAt: 0.01, BreakAt: 0.02, RevokeAt: revokeAt,
			Now: clk.now,
		})
	}

	drive := func(p *Policy) PolicyState {
		var d Decision
		for _, addr := range []string{"10.0.0.1:1", "203.0.113.1:1", "198.51.100.1:1", "192.0.2.1:1"} {
			i := req("acme")
			i.RemoteAddr = addr
			i.Model = addr // also trip novelty, to push the score up
			d = p.Check(i)
		}
		return d.State
	}

	if got := drive(mk(0)); got == StateRevoke {
		t.Error("auto-revoked with RevokeAt unset")
	}
	if got := drive(mk(0.05)); got != StateRevoke {
		t.Errorf("state = %s with RevokeAt configured, want revoke", got)
	}
}

func TestPolicy_StatusReportsWorstFirst(t *testing.T) {
	clk := newFakeClock()
	p := NewPolicy(PolicyConfig{
		Quota: QuotaLimits{Window: time.Minute, MaxTotalTokens: 100},
		Now:   clk.now,
	})

	p.Check(req("quiet"))
	p.RecordUsage("loud", Usage{InputTokens: 900})
	p.Check(req("loud"))

	st := p.Status()
	if len(st) != 2 {
		t.Fatalf("want 2 tenants, got %d", len(st))
	}
	if st[0].Tenant != "loud" {
		t.Errorf("worst tenant not first: %+v", st)
	}
	if st[0].State != StateCircuitBreak {
		t.Errorf("loud state = %s, want circuit_break", st[0].State)
	}
	if st[0].Tokens != 900 {
		t.Errorf("tokens in window = %d, want 900", st[0].Tokens)
	}
	if st[1].State != StateObserve {
		t.Errorf("quiet state = %s, want observe", st[1].State)
	}
}

// An empty tenant means an unauthenticated or malformed call the gateway
// rejects elsewhere; the ladder must not accumulate state under "".
func TestPolicy_EmptyTenantIsPassthrough(t *testing.T) {
	p := NewPolicy(PolicyConfig{Quota: QuotaLimits{Window: time.Minute, MaxTotalTokens: 1}})
	p.RecordUsage("", Usage{InputTokens: 10_000})
	p.RecordDenial("")
	if d := p.Check(RequestInfo{}); !d.Allow {
		t.Fatalf("empty-tenant request denied: %+v", d)
	}
	if len(p.Status()) != 0 {
		t.Errorf("empty tenant accumulated state: %+v", p.Status())
	}
}

func TestPolicy_ConcurrentCheckIsRaceFree(t *testing.T) {
	p := NewPolicy(PolicyConfig{
		Quota:   QuotaLimits{Window: time.Minute, MaxTotalTokens: 10_000},
		Anomaly: AnomalyConfig{Enabled: true, NoveltyTTL: time.Minute},
	})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				p.RecordUsage("acme", Usage{InputTokens: 1})
				p.Check(req("acme"))
				p.RecordDenial("acme")
				_ = p.Status()
			}
		}(i)
	}
	wg.Wait()
}

func hasSignal(sigs []Signal, name string) bool {
	for _, s := range sigs {
		if s.Name == name {
			return true
		}
	}
	return false
}

func statusFor(t *testing.T, p *Policy, tenant string) TenantStatus {
	t.Helper()
	for _, s := range p.Status() {
		if s.Tenant == tenant {
			return s
		}
	}
	t.Fatalf("no status for tenant %q", tenant)
	return TenantStatus{}
}

// Automatic revocation must be as sticky as the manual kind. Gating stickiness
// on a manual reason left an auto-revoked tenant walking back down the ladder
// one rung per cooldown, so a token cut off for a high anomaly score regained
// access by going quiet for five minutes.
func TestPolicy_AutoRevokeIsStickyToo(t *testing.T) {
	clk := newFakeClock()
	p := NewPolicy(PolicyConfig{
		Anomaly: AnomalyConfig{
			Enabled: true, NoveltyTTL: time.Hour,
			ConcurrentSourceNets: 1, ConcurrentWindow: time.Minute,
			BaselineMinSamples: 1000,
		},
		AlertAt: 0.01, BreakAt: 0.02, RevokeAt: 0.05,
		Cooldown: time.Minute,
		Now:      clk.now,
	})

	var d Decision
	for _, addr := range []string{"10.0.0.1:1", "203.0.113.1:1", "198.51.100.1:1", "192.0.2.1:1"} {
		i := req("acme")
		i.RemoteAddr = addr
		d = p.Check(i)
	}
	if d.State != StateRevoke {
		t.Fatalf("state = %s, want revoke", d.State)
	}

	// Long quiet stretches from a single clean network must not lift it.
	for i := 0; i < 10; i++ {
		clk.advance(10 * time.Minute)
		d = p.Check(req("acme"))
		if d.Allow || d.State != StateRevoke {
			t.Fatalf("auto-revocation lifted itself after %d quiet cycles: %+v", i+1, d)
		}
	}

	p.Clear("acme")
	if d := p.Check(req("acme")); !d.Allow {
		t.Errorf("Clear did not restore an auto-revoked tenant: %+v", d)
	}
}

// The ladder's own refusals must not feed the auth-failure signal. Counting
// them made enforcement self-amplifying: throttling a tenant raised its failure
// ratio, which raised its score, which escalated it further — laundering a
// budget overrun into "this token is being probed".
func TestPolicy_PolicyDenialsDoNotFeedTheAuthFailureSignal(t *testing.T) {
	clk := newFakeClock()
	p := NewPolicy(PolicyConfig{
		Quota: QuotaLimits{Window: time.Minute, MaxTotalTokens: 100},
		Anomaly: AnomalyConfig{
			Enabled:            true,
			AuthFailureRatio:   0.5,
			AuthFailureMin:     4,
			BaselineMinSamples: 1000,
			NoveltyTTL:         time.Hour,
		},
		ThrottleMinInterval: time.Hour, // every call past the first is refused
		CircuitBreakFor:     time.Hour,
		Now:                 clk.now,
	})

	// Blow the budget, then keep calling: each call is refused by the ladder.
	p.RecordUsage("acme", Usage{InputTokens: 500})
	var d Decision
	for i := 0; i < 20; i++ {
		d = p.Check(req("acme"))
		if d.Allow {
			t.Fatalf("call %d admitted while over budget", i)
		}
	}

	if hasSignal(d.Signals, SignalAuthFailureRate) {
		t.Errorf("the ladder's own denials tripped the auth-failure signal: %+v", d.Signals)
	}

	st := statusFor(t, p, "acme")
	if st.PolicyDenials == 0 {
		t.Error("policy denials not recorded")
	}
	if st.AuthDenials != 0 {
		t.Errorf("auth denials = %d, want 0 — no token-authorization failure occurred", st.AuthDenials)
	}

	// A genuine authorization failure still trips it.
	for i := 0; i < 10; i++ {
		p.RecordDenial("acme")
	}
	if d := p.Check(req("acme")); !hasSignal(d.Signals, SignalAuthFailureRate) {
		t.Errorf("real auth failures no longer trip the signal: %+v", d.Signals)
	}
}
