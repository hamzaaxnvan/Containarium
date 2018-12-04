package server

import (
	"bufio"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The box address the eBPF program validates a box's packets against (#2618)
// must come from the HOST, never from the box: Incus's instance state reports
// the addresses the guest configured on itself, which root in the box controls,
// so a box could pick the address it wants to be "validated" as. Two host-side
// sources exist, in order of preference:
//
//  1. the NIC device's `ipv4.address` — a static assignment in the instance's
//     (profile-expanded) device config, which only the host's Incus API changes;
//  2. the bridge's DHCP lease for the NIC's hardware address — written by the
//     host's dnsmasq (`<incus dir>/networks/<bridge>/dnsmasq.leases`). A box can
//     ask DHCP for an address, but dnsmasq hands out only one from the pool, never
//     one already leased to another MAC, and the box cannot edit the file.
//
// A box whose address is known from neither has box_ip 0: the program then
// cannot validate its source, so frames from it never seed an inbound flow,
// and it cannot answer inbound connections under enforce. The guest-reported
// address is deliberately NOT a fallback.

// defaultIncusLeaseDir is where Incus keeps each managed bridge's dnsmasq lease
// file, under the same data dir as its unix socket; leaseDirFor derives it from
// the client's actual data dir.
const defaultIncusLeaseDir = "/var/lib/incus/networks"

// leaseDirFor is the lease directory under an Incus data dir.
func leaseDirFor(incusDataDir string) string { return filepath.Join(incusDataDir, "networks") }

// hostNIC is what the enforcer needs to know about an instance's primary NIC
// device, read from host-side config.
type hostNIC struct {
	Network  string // the Incus network (bridge) the NIC is on; "" when unknown
	HWAddr   string // the NIC's MAC, lower-case; "" when unknown
	StaticIP netip.Addr
}

// primaryNICDevice picks the instance's primary NIC from its expanded device
// map: `eth0` when it is a nic, else the lexicographically first nic device, so
// the choice is deterministic for an instance whose NIC has another name.
// Returns the device name and config, or "" when the instance has no nic.
func primaryNICDevice(devices map[string]map[string]string) (string, map[string]string) {
	if d, ok := devices["eth0"]; ok && d["type"] == "nic" {
		return "eth0", d
	}
	var names []string
	for name, d := range devices {
		if d["type"] == "nic" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	sort.Strings(names)
	return names[0], devices[names[0]]
}

// hostNICFor derives the primary NIC's host-side facts from an instance's
// config (for the `volatile.<nic>.hwaddr` Incus assigns) and its expanded
// devices (for `network`/`parent` and `ipv4.address`).
func hostNICFor(config map[string]string, devices map[string]map[string]string) hostNIC {
