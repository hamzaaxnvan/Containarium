package server

import (
	"context"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// ipResolver is the slice of *net.Resolver the stdlib path needs, so it can
// be faked in tests. *net.Resolver satisfies it.
type ipResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// ttlUnknown marks an answer whose resolver cannot report record TTLs (the
// stdlib path); such a name is refreshed on the maxDomainRefresh cap.
const ttlUnknown time.Duration = -1

// maxDomainRefresh caps a name's refresh interval: it refreshes on
// min(record TTL, 60s) (#2379, coding-cli-egress-allowlist.md "Domains").
const maxDomainRefresh = 60 * time.Second

// minDomainRefresh floors the interval so a TTL-0 record cannot hot-loop the
// refresh (and the policy-store read that precedes it).
const minDomainRefresh = time.Second

// dnsAnswer is one name's A+AAAA lookup result.
type dnsAnswer struct {
	Addrs []netip.Addr
	TTL   time.Duration // smallest record TTL in the answer, or ttlUnknown
}

// addrTTLResolver resolves a name to both address families plus a TTL.
type addrTTLResolver interface {
	LookupAddrsTTL(ctx context.Context, host string) (dnsAnswer, error)
}

// stdlibTTLResolver adapts an ipResolver (net.Resolver exposes no TTL).
type stdlibTTLResolver struct{ r ipResolver }

func (s stdlibTTLResolver) LookupAddrsTTL(ctx context.Context, host string) (dnsAnswer, error) {
	addrs, err := s.r.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return dnsAnswer{}, err
	}
	return dnsAnswer{Addrs: addrs, TTL: ttlUnknown}, nil
}

// domainEntry is one name's cached state.
type domainEntry struct {
	current  []netip.Addr             // last successful answer, sorted
	retained map[netip.Addr]time.Time // rotated-out addresses -> kept until
	interval time.Duration            // min(TTL, 60s), floored
	due      time.Time                // next refresh
}

// DomainResolver maintains a cache of egress_domains → current addresses
// (#315 Phase C, TTL-aware and dual-stack since #2379). Each name refreshes on
// min(record TTL, 60s). When a lookup succeeds with a different set, the
// rotated-out addresses stay for two refresh intervals so a rotation does not
// cut a live connection; when a lookup fails, the previous set is kept.
// Addrs is the dual-stack view; IPs is the IPv4-only view the current
// (IPv4-only) BPF enforcer consumes.
type DomainResolver struct {
	resolver addrTTLResolver
	now      func() time.Time
	mu       sync.RWMutex
	cache    map[string]*domainEntry
}

// NewDomainResolver builds a resolver. A nil ipResolver uses the system's
// TTL-aware DNS path (falling back to net.DefaultResolver); a non-nil one is
// used as-is with TTLs unknown.
func NewDomainResolver(r ipResolver) *DomainResolver {
	if r == nil {
		return newDomainResolver(newSystemTTLResolver(), time.Now)
	}
