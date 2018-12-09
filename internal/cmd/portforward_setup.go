//go:build !containarium_client

package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/network"
	"github.com/spf13/cobra"
)

var (
	portforwardCaddyIP    string
	portforwardAutoDetect bool
)

var portforwardSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Setup port forwarding rules for Caddy",
	Long: `Setup iptables port forwarding rules to route external traffic to Caddy.

This command will:
1. Enable IP forwarding in the kernel
2. Add PREROUTING rules to forward ports 80 and 443 to Caddy
3. Add MASQUERADE rule for return traffic

The rules exclude traffic from Caddy's own IP to allow outbound HTTPS
connections (e.g., to Let's Encrypt servers).

Examples:
  # Setup with explicit Caddy IP
  containarium portforward setup --caddy-ip 10.0.3.111

  # Setup with auto-detection (requires Incus)
  containarium portforward setup --auto`,
