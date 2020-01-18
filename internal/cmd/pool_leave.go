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
restart the daemon standalone if it's running. Idempotent — safe to re-run.

Does NOT stop your containers; the daemon keeps serving them locally. Run on
the host, as root. Use --dry-run to preview.`,
	RunE: runPoolLeave,
}

func init() {
	poolCmd.AddCommand(poolLeaveCmd)
	poolLeaveCmd.Flags().BoolVar(&poolLeaveDryRun, "dry-run", false, "Print what would happen, then exit (no changes)")
}

func runPoolLeave(cmd *cobra.Command, args []string) error {
	if poolLeaveDryRun {
		fmt.Println("Would leave the pool:")
		fmt.Printf("  - systemctl disable --now containarium-tunnel  (deregisters from the sentinel)\n")
		fmt.Printf("  - rm %s\n", tunnelUnitPath)
		fmt.Printf("  - rm %s   (tunnel-handshake token, #935)\n", tunnelTokenSecretFile)
		fmt.Printf("  - rm %s   (daemon returns to standalone)\n", daemonDropIn)
		fmt.Printf("  - systemctl daemon-reload && systemctl try-restart containarium\n")
		fmt.Println("  (dry-run: nothing changed; the daemon + your containers are untouched)")
		return nil
	}

	if os.Geteuid() != 0 {
		return fmt.Errorf("this command requires root privileges (use sudo), or pass --dry-run to preview")
	}

	// 1. Stop + disable the tunnel — only if the unit exists (idempotent;
