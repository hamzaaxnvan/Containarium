package server

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

// fakeIPResolver returns canned results per host.
type fakeIPResolver struct {
	results map[string][]netip.Addr
	errs    map[string]error
	calls   map[string]int
}

func (f *fakeIPResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[host]++
	if err := f.errs[host]; err != nil {
		return nil, err
	}
	return f.results[host], nil
}

func addrs(ss ...string) []netip.Addr {
	out := make([]netip.Addr, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParseAddr(s)
