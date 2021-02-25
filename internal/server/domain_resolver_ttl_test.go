package server

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"
)

// fakeTTLResolver is fake DNS that reports a record TTL with each answer (#2379).
type fakeTTLResolver struct {
	answers map[string]dnsAnswer
	errs    map[string]error
	calls   map[string]int
}

func (f *fakeTTLResolver) LookupAddrsTTL(_ context.Context, host string) (dnsAnswer, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[host]++
	if err := f.errs[host]; err != nil {
		return dnsAnswer{}, err
	}
	return f.answers[host], nil
}

// resolverClock is a settable clock for the resolver's refresh schedule.
type resolverClock struct{ t time.Time }

func (c *resolverClock) now() time.Time          { return c.t }
func (c *resolverClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newResolverClock() *resolverClock {
	return &resolverClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func addrStrings(as []netip.Addr) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.String()
	}
	return out
}

func TestDomainResolver_RefreshIntervalIsMinTTLAnd60s(t *testing.T) {
	cases := []struct {
		name string
		ttl  time.Duration
		want time.Duration
	}{
		{"short TTL drives the refresh", 20 * time.Second, 20 * time.Second},
		{"long TTL is capped at 60s", 300 * time.Second, 60 * time.Second},
		{"unknown TTL (stdlib path) uses 60s", ttlUnknown, 60 * time.Second},
		{"TTL 0 is floored, never a hot loop", 0, minDomainRefresh},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := newResolverClock()
			f := &fakeTTLResolver{answers: map[string]dnsAnswer{
				"a.example": {Addrs: addrs("192.0.2.1"), TTL: tc.ttl},
			}}
			r := newDomainResolver(f, clk.now)
			r.Refresh(context.Background(), []string{"a.example"})
			if got := r.NextRefreshDelay(); got != tc.want {
				t.Fatalf("NextRefreshDelay = %v, want %v", got, tc.want)
			}
			// Not due yet: a Refresh before the interval elapses does not re-query.
			clk.advance(tc.want - time.Millisecond)
			r.Refresh(context.Background(), []string{"a.example"})
			if f.calls["a.example"] != 1 {
				t.Fatalf("re-queried before the interval: %d calls", f.calls["a.example"])
			}
			// Due: the next Refresh re-queries.
			clk.advance(time.Millisecond)
			r.Refresh(context.Background(), []string{"a.example"})
			if f.calls["a.example"] != 2 {
				t.Fatalf("not re-queried once due: %d calls", f.calls["a.example"])
			}
		})
	}
}

func TestDomainResolver_NextRefreshDelay_EarliestDueName(t *testing.T) {
	clk := newResolverClock()
	f := &fakeTTLResolver{answers: map[string]dnsAnswer{
		"slow.example": {Addrs: addrs("192.0.2.1"), TTL: 50 * time.Second},
		"fast.example": {Addrs: addrs("192.0.2.2"), TTL: 10 * time.Second},
	}}
	r := newDomainResolver(f, clk.now)
	if got := r.NextRefreshDelay(); got != 60*time.Second {
		t.Fatalf("empty resolver delay = %v, want the 60s cap", got)
	}
	r.Refresh(context.Background(), []string{"slow.example", "fast.example"})
	clk.advance(4 * time.Second)
	if got := r.NextRefreshDelay(); got != 6*time.Second {
		t.Fatalf("delay = %v, want 6s (fast.example due first)", got)
	}
	clk.advance(6 * time.Second)
	r.Refresh(context.Background(), []string{"slow.example", "fast.example"})
	if f.calls["fast.example"] != 2 || f.calls["slow.example"] != 1 {
		t.Fatalf("only the due name should be re-queried, calls = %v", f.calls)
	}
