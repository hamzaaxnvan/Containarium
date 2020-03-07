package cmd

import (
	"fmt"
	"strings"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// passthroughRouteCmd represents the passthrough-route command group.
//
// Named distinctly from the existing `passthrough` command (local iptables
// DNAT on the host running the CLI, pkg/core/network — see passthrough.go):
// this one talks to the daemon's AddPassthroughRoute / ListPassthroughRoutes
// / DeletePassthroughRoute RPCs, so it works against a remote box the CLI
// has no shell access to, the same way `route` does for HTTPS proxy routes.
// See #1550.
var passthroughRouteCmd = &cobra.Command{
	Use:   "passthrough-route",
	Short: "Manage daemon-level TCP/UDP passthrough routes",
	Long: `Manage TCP/UDP passthrough routes on the Containarium daemon.

Passthrough routes forward traffic directly to a container without TLS
termination (raw L4), unlike 'route' which is an HTTPS reverse-proxy
mapping. These routes are created via the daemon's API — use this command
(rather than 'passthrough', which edits iptables on the local host) to
manage passthrough routes on a remote box.
