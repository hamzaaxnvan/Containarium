package cmd

import (
	"github.com/spf13/cobra"
)

// backendsCmd is the parent for "containarium backends list" and any
// future per-backend operations. Backends are the individual host
// daemons (local + tunnel-connected peers) that make up a multi-host
// Containarium fleet — exposed via /v1/backends on the platform daemon.
var backendsCmd = &cobra.Command{
	Use:   "backends",
	Short: "Inspect cluster backends (local daemon + tunnel peers)",
	Long: `Manage and inspect the backend hosts that make up a Containarium
fleet. The "local" backend is the daemon you're talking to; "tunnel"
backends are peer hosts connected via outbound tunnel (e.g. bare-metal
GPU nodes, secondary regions).

Examples:
  # List all backends
  containarium backends list --server <host:port>`,
}

func init() {
	rootCmd.AddCommand(backendsCmd)
}
