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
