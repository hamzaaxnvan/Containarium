//go:build !containarium_client

package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var portforwardShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current port forwarding rules",
	Long: `Display the current iptables NAT rules for port forwarding.

Shows PREROUTING rules (inbound traffic forwarding) and POSTROUTING rules
(return traffic masquerading).

Examples:
  containarium portforward show`,
	RunE: runPortforwardShow,
}

func init() {
	portforwardCmd.AddCommand(portforwardShowCmd)
}

func runPortforwardShow(cmd *cobra.Command, args []string) error {
	fmt.Println("=== Port Forwarding Rules ===")
	fmt.Println()

	// Check if iptables is available
	if !checkIptablesAvailable() {
		return fmt.Errorf("iptables is not available on this system")
	}

	// Show PREROUTING rules
	fmt.Println("PREROUTING (inbound traffic forwarding):")
	fmt.Println("-----------------------------------------")
