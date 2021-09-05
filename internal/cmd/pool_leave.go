//go:build !windows && !containarium_client

package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

// pool leave — the inverse of `pool join`. Deregisters this host from the
// pool by stopping the tunnel (the sentinel drops the peer once the tunnel
// connection ends), removes the tunnel unit and the --pool drop-in, and
// returns the daemon to standalone. Idempotent.
//
// MVP scope: it does NOT stop the host's containers — your workloads keep
// running on the now-standalone daemon (leaving the POOL ≠ decommissioning
// the host; to fully tear down, `service uninstall` separately). Graceful
// workload drain/migration is a follow-up (prd/oss/byo-compute-pool-join.md).

var poolLeaveDryRun bool

var poolLeaveCmd = &cobra.Command{
	Use:   "leave",
	Short: "Remove THIS host from the pool (stop the tunnel, drop --pool, go standalone)",
	Long: `Leave the pool: stop + disable the tunnel (which deregisters this host from
the sentinel), remove the tunnel unit and the --pool daemon drop-in, and
