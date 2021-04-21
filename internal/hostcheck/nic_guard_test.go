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
