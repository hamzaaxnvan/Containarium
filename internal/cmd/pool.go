package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// poolCmd is the parent for the per-tenant BYO-compute pool commands —
// turning a spare host into a member of YOUR OWN pool and scheduling your
// own workloads across it (prd/oss/byo-compute-pool-join.md). This is the
// single-operator "I have more than one machine" path: no cross-tenant
// sharing, your data stays on your hosts.
//
// `pool list` reads the daemon's view of the pool (its discovered
// backends); `pool join` (host-side) turns a fresh host into a member.
var poolCmd = &cobra.Command{
	Use:   "pool",
	Short: "Manage your BYO-compute pool (add your own hosts, see members)",
	Long: `Manage the compute pool made up of hosts YOU bring — a single operator
pooling more than one machine so the daemon can schedule your own
workloads across them. Your workloads and data stay on your hosts; this
is not cross-tenant sharing.

Examples:
  # Show the pool's members + health (reads the daemon you point --server at)
  containarium pool list --server http://host:8080

  # Turn THIS host into a pool member (run on the new host, as root)
  sudo containarium pool join --sentinel sentinel.example.com:443 \
    --pool prod --token <scoped-join-token>`,
}

