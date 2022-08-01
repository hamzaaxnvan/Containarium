package sshgate

import (
	"testing"
	"time"
)

// TestClientCredentialFailureRetainsRiskWithoutOutage protects pass-through passwords.
func TestClientCredentialFailureRetainsRiskWithoutOutage(t *testing.T) {
	f := newGateFixture(t, func(p *Policy) { p.Budget = 2 })
	if !f.g.Complete(f.take(t, 1), ClientAuthRejected) {
		t.Fatal("client denial was not settled")
	}
	l := f.take(t, 1)
	if l.Probe {
		t.Fatal("bad client credential declared a backend outage")
	}
	denied(t, f.g, f.key, 1, 1, BudgetExhausted)
	f.g.Complete(l, SetupSucceeded)
	denied(t, f.g, f.key, 1, 2, BudgetExhausted)
}

// TestCheckpointRetainsPendingRiskAndExclusion does not trust a lost setup as ready.
func TestCheckpointRetainsPendingRiskAndExclusion(t *testing.T) {
	f := newGateFixture(t, func(p *Policy) { p.Budget = 3 })
	start := f.now
	f.g.Complete(f.take(t, 1), ProxyAuthRejected)
	f.g.HintRecovery(f.key, 1)
	old := f.take(t, 2)
	rows := f.g.Checkpoint()
	if len(rows) != 1 || len(rows[0].Risks) != 2 {
		t.Fatalf("lost retained or pending risk: %+v", rows)
	}
	fresh := newGateFixture(t, func(p *Policy) { p.Budget = 3 })
	// Restore is intentionally only permitted before inventory is registered.
	g, err := New(fresh.g.policy, func() time.Time { return f.now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Restore(rows); err != nil {
		t.Fatal(err)
	}
	if g.Complete(old, SetupSucceeded) {
