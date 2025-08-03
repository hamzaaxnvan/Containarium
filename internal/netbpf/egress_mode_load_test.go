//go:build ebpf_load

package netbpf

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/cilium/ebpf/perf"

	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Three more routed addresses on the host side of the veth pair, outside the
// /30 and distinct from ebpfLoadPeerBoxAddr: one stands in for another tenant's
// box (registered in ip_tenant under a different tenant id), one for an
// internet destination that is not on the box's egress allow-list, and the
// cloud metadata address, which the program checks before either of them.
// Binding the metadata address on the host veth shadows the runner's own
// metadata service for the few seconds the test runs; cleanup removes it.
const (
	ebpfLoadOtherTenantAddr = "10.250.113.1"
	ebpfLoadInternetAddr    = "10.250.114.1"
	ebpfLoadMetadataAddr    = "169.254.169.254"
	ebpfLoadOtherTenant     = uint32(2)
	ebpfLoadEgressModePort  = 8676
)

// TestAttachedProgram_EgressModeSeparatesAllowListFromCrossTenant proves the
// separate egress mode on a real kernel with real TCP, for both splits:
//
