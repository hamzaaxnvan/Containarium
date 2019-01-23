//go:build !windows

package hostcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// NICGuardProbe is what the tenant-isolation posture check reads from the
// host, injected so the check is testable without Incus:
//   - the Incus firewall driver and whether the network_bridge_acl_devices
//     API extension is present (both guards need them),
//   - the running tenant containers whose eth0 carries no
//     containarium-tenant-* ACL (unguarded), and the count that do.
type NICGuardProbe func() (nicGuardFacts, error)

type nicGuardFacts struct {
	firewallDriver string
	hasExtension   bool
	guarded        int
	unguarded      []string
}

const (
	nicGuardProbeTimeout      = 10 * time.Second
	nicGuardRequiredDriver    = "nftables"
	nicGuardRequiredExtension = "network_bridge_acl_devices"
	nicGuardACLPrefix         = "containarium-tenant-"
)

// nicGuardCheck is the tenant ↔ tenant boundary as the host shows it
// (docs/architecture/tenant-network-guard.md): read from Incus itself, not
// from the daemon's own status, so a daemon that believes it is guarding
// and a host that is not disagree visibly. Red when the host cannot carry
// bridge NIC ACLs, or when any running tenant container has no tenant ACL
// on its NIC.
func nicGuardCheck(p posturePaths) Check {
	c := Check{Name: "tenant containers carry a tenant-guard NIC ACL (tenant ↔ tenant isolation)"}
	if p.nicGuardProbe == nil {
		c.Detail = "could not determine: no probe configured"
		return c
