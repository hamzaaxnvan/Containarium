package server

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #2618 B1: the address the program validates a box against must come from the
// host, never from the guest's own report. These pin the two host sources.

func TestParseDnsmasqLeases(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	in := strings.Join([]string{
		"1800003600 00:16:3e:aa:bb:cc 10.100.0.10 alice-container 01:00:16:3e:aa:bb:cc", // live
		"1700000000 00:16:3e:dd:ee:ff 10.100.0.11 stale-container *",                    // expired
		"0 00:16:3E:11:22:33 10.100.0.12 pinned-container *",                            // permanent, upper-case MAC
		"not-a-number 00:16:3e:44:55:66 10.100.0.13 bad *",                              // malformed expiry
		"1800003600 00:16:3e:77:88:99 fd00::1 v6-only *",                                // not IPv4
		"short",
	}, "\n")
	got := parseDnsmasqLeases(strings.NewReader(in), now)
	want := map[string]netip.Addr{
		"00:16:3e:aa:bb:cc": netip.MustParseAddr("10.100.0.10"),
		"00:16:3e:11:22:33": netip.MustParseAddr("10.100.0.12"),
	}
	if len(got) != len(want) {
		t.Fatalf("leases = %v, want %v", got, want)
	}
	for mac, ip := range want {
		if got[mac] != ip {
			t.Errorf("lease[%s] = %v, want %v", mac, got[mac], ip)
		}
	}
}

func TestPrimaryNICDevice(t *testing.T) {
	devs := map[string]map[string]string{
		"root":  {"type": "disk", "path": "/"},
		"eth1":  {"type": "nic", "network": "other"},
		"eth0":  {"type": "nic", "network": "incusbr0"},
		"proxy": {"type": "proxy"},
	}
	if name, d := primaryNICDevice(devs); name != "eth0" || d["network"] != "incusbr0" {
		t.Errorf("primary = %q %v, want eth0 on incusbr0", name, d)
	}
	delete(devs, "eth0")
	if name, _ := primaryNICDevice(devs); name != "eth1" {
		t.Errorf("primary without eth0 = %q, want the first nic by name (eth1)", name)
	}
	delete(devs, "eth1")
	if name, d := primaryNICDevice(devs); name != "" || d != nil {
		t.Errorf("primary with no nic = %q %v, want none", name, d)
	}
	// A non-nic device named eth0 is not the NIC.
	if name, _ := primaryNICDevice(map[string]map[string]string{"eth0": {"type": "disk"}, "net0": {"type": "nic"}}); name != "net0" {
		t.Errorf("primary = %q, want net0 (eth0 is not a nic here)", name)
	}
}

func TestHostNICFor(t *testing.T) {
	cfg := map[string]string{"volatile.eth0.hwaddr": "00:16:3E:AA:BB:CC", "volatile.eth0.host_name": "vethabc"}
	nic := hostNICFor(cfg, map[string]map[string]string{"eth0": {"type": "nic", "network": "incusbr0", "ipv4.address": "10.100.0.5"}})
	if nic.Network != "incusbr0" || nic.HWAddr != "00:16:3e:aa:bb:cc" || nic.StaticIP != netip.MustParseAddr("10.100.0.5") {
		t.Errorf("hostNICFor = %+v", nic)
