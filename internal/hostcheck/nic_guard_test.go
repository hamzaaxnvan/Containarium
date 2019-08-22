//go:build !windows

package hostcheck

import (
	"errors"
	"strings"
	"testing"
)

func TestNICGuardCheck(t *testing.T) {
	tests := []struct {
		name   string
		facts  nicGuardFacts
		err    error
		wantOK bool
		want   string // substring of Detail
	}{
		{"healthy host", nicGuardFacts{firewallDriver: "nftables", hasExtension: true, guarded: 3}, nil, true, "3 tenant container(s) guarded"},
		{"xtables driver", nicGuardFacts{firewallDriver: "xtables", hasExtension: true, guarded: 1}, nil, false, `driver is "xtables"`},
		{"incus 6.0.0", nicGuardFacts{firewallDriver: "nftables", hasExtension: false}, nil, false, "lacks the network_bridge_acl_devices"},
		{"unguarded tenants", nicGuardFacts{firewallDriver: "nftables", hasExtension: true, guarded: 1, unguarded: []string{"bob-container", "alice-container"}}, nil, false, "2 running tenant container(s) without"},
		{"probe error", nicGuardFacts{}, errors.New("incus query /1.0: no socket"), false, "could not determine"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := posturePaths{nicGuardProbe: func() (nicGuardFacts, error) { return tc.facts, tc.err }}
			c := nicGuardCheck(p)
			if c.OK != tc.wantOK {
				t.Errorf("OK = %v, want %v (%s)", c.OK, tc.wantOK, c.Detail)
			}
			if !strings.Contains(c.Detail, tc.want) {
				t.Errorf("Detail = %q, want containing %q", c.Detail, tc.want)
			}
		})
	}
	// Unguarded names are sorted and capped so a big host stays readable.
