//go:build !containarium_client

package cmd

import (
	"github.com/spf13/cobra"
)

// passthroughCmd represents the passthrough command
var passthroughCmd = &cobra.Command{
	Use:   "passthrough",
	Short: "Manage TCP/UDP passthrough routes via iptables",
	Long: `Manage TCP/UDP passthrough routes for direct port forwarding to containers.

