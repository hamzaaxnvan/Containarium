package incus

import (
	"testing"

	"github.com/lxc/incus/v7/shared/api"
)

func globalInet(addr string) []api.InstanceStateNetworkAddress {
	return []api.InstanceStateNetworkAddress{
		{Family: "inet", Scope: "global", Address: addr},
	}
}

// TestPrimaryNetworkInterface_VMWithK3sBridges is the regression test for
// containarium#1878: a VM running a k3s server has its own real NIC
// (enp5s0-style predictable name) alongside k3s's cni0/flannel.1 bridges.
// The real NIC must always win, deterministically — not by luck of Go's
// unordered map iteration.
func TestPrimaryNetworkInterface_VMWithK3sBridges(t *testing.T) {
	network := map[string]api.InstanceStateNetwork{
		"cni0":      {Addresses: globalInet("10.42.0.1")},
		"flannel.1": {Addresses: globalInet("10.42.0.0")},
