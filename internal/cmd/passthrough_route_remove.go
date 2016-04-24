package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	passthroughRouteRemovePort     int
	passthroughRouteRemoveProtocol string
)

var passthroughRouteRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a daemon-level TCP/UDP passthrough route",
	Long: `Remove a TCP/UDP passthrough route from the Containarium daemon by its
external port and protocol.

Examples:
  # Remove a TCP passthrough route
  containarium passthrough-route remove --port 9443 --server <host:port>

  # Remove a UDP passthrough route
  containarium passthrough-route remove --port 53 --protocol udp --server <host:port>`,
	Aliases: []string{"rm", "delete"},
	RunE:    runPassthroughRouteRemove,
}

func init() {
	passthroughRouteCmd.AddCommand(passthroughRouteRemoveCmd)

	passthroughRouteRemoveCmd.Flags().IntVar(&passthroughRouteRemovePort, "port", 0, "External port to remove (required)")
	passthroughRouteRemoveCmd.Flags().StringVar(&passthroughRouteRemoveProtocol, "protocol", "tcp", "Protocol: tcp or udp")

	_ = passthroughRouteRemoveCmd.MarkFlagRequired("port")
}

func runPassthroughRouteRemove(cmd *cobra.Command, args []string) error {
	if serverAddr == "" {
		return fmt.Errorf("--server is required")
	}
