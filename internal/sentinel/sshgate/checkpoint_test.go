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
