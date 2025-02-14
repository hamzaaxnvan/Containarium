package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/netpolicy"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// #2618 B1 + S4: gather() takes the address the program validates a box against
// (view.HostIP -> policy_cfg.box_ip) from the host — the NIC's static
// ipv4.address, else the bridge's DHCP lease — and never from c.IPAddress alone,
// which is what the guest configured on itself. A lease counts only when it
// agrees with the guest-reported address: a neighbour can move a lease with a
// forged DHCP release/request naming another box's MAC, and the guest can
// renumber itself; either disagreement makes the address UNKNOWN (fail closed),
// never a guess from one side.
func TestGather_BoxAddressIsHostKnownNotGuestReported(t *testing.T) {
	veth := firstHostIface(t)
	const mac = "00:16:3e:aa:bb:cc"
	dataDir := t.TempDir() // stands in for the Incus data dir; leases live under <data dir>/networks
	leaseDir := filepath.Join(dataDir, "networks")
	if err := os.MkdirAll(filepath.Join(leaseDir, "testbr0"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLease := func(entries string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(leaseDir, "testbr0", "dnsmasq.leases"), []byte(entries), 0o644); err != nil {
			t.Fatal(err)
		}
	}
