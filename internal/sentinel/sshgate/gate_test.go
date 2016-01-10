package sshgate

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// gateFixture owns a deterministic clock and a single registered backend.
type gateFixture struct {
	g   *Gate
	now time.Time
	key Key
}

// newGateFixture supplies explicit synthetic limits, unrelated to production defaults.
func newGateFixture(t *testing.T, change ...func(*Policy)) *gateFixture {
	t.Helper()
	f := &gateFixture{now: time.Unix(1000, 0), key: Key{"backend-a", "egress-a"}}
	p := Policy{InitialDelay: time.Second, MaxDelay: 8 * time.Second, Window: time.Minute, LeaseTimeout: 5 * time.Second, Budget: 8, MaxBackends: 2, MaxPending: 8}
	for _, apply := range change {
		apply(&p)
	}
	var err error
	f.g, err = New(p, func() time.Time { return f.now }, func() float64 { return 0.5 })
	if err != nil {
		t.Fatal(err)
	}
	if err := f.g.Register(f.key, 1); err != nil {
		t.Fatal(err)
	}
	return f
}

// take requires admission without reaching into the controller's internal state.
func (f *gateFixture) take(t *testing.T, cost int) Lease {
	t.Helper()
	l, err := f.g.Acquire(f.key, 1, cost)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// denied checks typed failure and exposes the reported retry boundary.
func denied(t *testing.T, g *Gate, key Key, route uint64, cost int, reason Reason) time.Time {
	t.Helper()
	_, err := g.Acquire(key, route, cost)
	var d *Denied
	if !errors.As(err, &d) || d.Reason != reason {
		t.Fatalf("reason=%v, error=%v; want %v", d, err, reason)
	}
	return d.RetryAt
}

// TestGateBackoffAndSSHProbe preserves one probe and ignores an old healthy setup.
func TestGateBackoffAndSSHProbe(t *testing.T) {
	f := newGateFixture(t)
	first, old := f.take(t, 1), f.take(t, 1)
	if !f.g.Complete(first, DialRefused) || !f.g.Complete(old, SetupSucceeded) {
		t.Fatal("normal setup completion rejected")
	}
	for _, delay := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second} {
		d := denied(t, f.g, f.key, 1, 1, Cooldown)
		if d.Sub(f.now) != delay {
			t.Fatalf("delay=%s want %s", d.Sub(f.now), delay)
		}
		f.now = d
		probe := f.take(t, 1)
		if !probe.Probe {
			t.Fatal("recovery admission is not a probe")
		}
		denied(t, f.g, f.key, 1, 1, ProbeBusy)
		if !f.g.Complete(probe, DialRefused) {
			t.Fatal("probe failure rejected")
		}
	}
	f.g.HintRecovery(f.key, 1)
	probe := f.take(t, 1)
	// There is deliberately no TCP-ready transition: the lease remains held.
	denied(t, f.g, f.key, 1, 1, ProbeBusy)
	if !f.g.Complete(probe, SetupSucceeded) || f.g.Complete(probe, SetupFailed) {
		t.Fatal("SSH-ready promotion/duplicate result broken")
	}
	if f.take(t, 1).Probe {
		t.Fatal("healthy admission remains a probe")
	}
}

// TestGateBudgetSurvivesRecovery keeps weighted failures through healthy success.
func TestGateBudgetSurvivesRecovery(t *testing.T) {
	f := newGateFixture(t, func(p *Policy) { p.Budget = 3 })
	start := f.now
	f.g.Complete(f.take(t, 1), ProxyAuthRejected)
	f.now = start.Add(time.Second)
	f.g.Complete(f.take(t, 1), SetupSucceeded)
	second := f.take(t, 2)
	denied(t, f.g, f.key, 1, 1, BudgetExhausted)
	f.g.Complete(second, SetupFailed)
