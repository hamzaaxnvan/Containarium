//go:build !containarium_client

package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/footprintai/containarium/pkg/core/network"
	"github.com/spf13/cobra"
)

var passthroughListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all passthrough routes",
	Long: `List all TCP/UDP passthrough routes currently configured via iptables.

Shows the external port, target IP:port, protocol, and status for each route.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPassthroughList()
	},
}

func init() {
	passthroughCmd.AddCommand(passthroughListCmd)
}

func runPassthroughList() error {
	// Check if iptables is available
	if !network.CheckIPTablesAvailable() {
		return fmt.Errorf("iptables not available on this system")
	}

	// Create passthrough manager (network CIDR not needed for listing)
