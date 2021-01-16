//go:build !containarium_client

package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/network"
	"github.com/spf13/cobra"
)

var (
	portforwardRemoveCaddyIP    string
	portforwardRemoveAutoDetect bool
)

var portforwardRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove port forwarding rules for Caddy",
	Long: `Remove iptables port forwarding rules.

This command will remove:
1. PREROUTING rules for ports 80 and 443
2. MASQUERADE rule for return traffic

Note: This does NOT disable IP forwarding, as it may be used by other services.

Examples:
  # Remove with explicit Caddy IP
  containarium portforward remove --caddy-ip 10.0.3.111

  # Remove with auto-detection (requires Incus)
  containarium portforward remove --auto`,
	RunE: runPortforwardRemove,
}

func init() {
	portforwardCmd.AddCommand(portforwardRemoveCmd)
	portforwardRemoveCmd.Flags().StringVar(&portforwardRemoveCaddyIP, "caddy-ip", "", "IP address of the Caddy container")
	portforwardRemoveCmd.Flags().BoolVar(&portforwardRemoveAutoDetect, "auto", false, "Auto-detect Caddy container IP from Incus")
}

func runPortforwardRemove(cmd *cobra.Command, args []string) error {
	// Check if iptables is available
	if !network.CheckIPTablesAvailable() {
		return fmt.Errorf("iptables is not available on this system")
	}

	caddyIP := portforwardRemoveCaddyIP

	// Auto-detect if requested
	if portforwardRemoveAutoDetect {
		if caddyIP != "" {
			return fmt.Errorf("cannot use both --caddy-ip and --auto")
		}

		fmt.Println("Auto-detecting Caddy container IP...")
