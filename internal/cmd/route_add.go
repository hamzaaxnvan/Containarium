package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var (
	routeAddTarget      string
	routeAddContainer   string
	routeAddDescription string
)

var routeAddCmd = &cobra.Command{
	Use:   "add <domain>",
	Short: "Add a proxy route",
	Long: `Add a new proxy route (domain to container mapping).

The route maps an external domain to an internal container IP:port.

Examples:
  # Add a route
  containarium route add test.example.com --target 10.0.3.136:8080 --server <host:port>

  # Add with container name and description
  containarium route add api.example.com --target 10.0.3.140:3000 \
    --container myapp-container \
    --description "API server"`,
	Args: cobra.ExactArgs(1),
	RunE: runRouteAdd,
}

func init() {
	routeCmd.AddCommand(routeAddCmd)

	routeAddCmd.Flags().StringVarP(&routeAddTarget, "target", "t", "", "Target IP:port (required)")
	routeAddCmd.Flags().StringVarP(&routeAddContainer, "container", "c", "", "Associated container name (optional)")
	routeAddCmd.Flags().StringVarP(&routeAddDescription, "description", "d", "", "Route description (optional)")

