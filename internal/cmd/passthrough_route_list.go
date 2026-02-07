package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var passthroughRouteListFormat string

var passthroughRouteListCmd = &cobra.Command{
	Use:   "list",
	Short: "List daemon-level TCP/UDP passthrough routes",
	Long: `List all TCP/UDP passthrough routes configured on the Containarium daemon.

Examples:
  # List all passthrough routes
  containarium passthrough-route list --server <host:port>

  # List in JSON format
  containarium passthrough-route list --format json --server <host:port>`,
	Aliases: []string{"ls"},
	RunE:    runPassthroughRouteList,
}

func init() {
	passthroughRouteCmd.AddCommand(passthroughRouteListCmd)

	passthroughRouteListCmd.Flags().StringVarP(&passthroughRouteListFormat, "format", "f", "table", "Output format: table, json")
}

func runPassthroughRouteList(cmd *cobra.Command, args []string) error {
	if serverAddr == "" {
		return fmt.Errorf("--server is required")
	}

	apiClient, err := newRouteClient()
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer func() { _ = apiClient.Close() }()

	routes, totalCount, err := apiClient.ListPassthroughRoutes()
	if err != nil {
		return fmt.Errorf("failed to list passthrough routes: %w", err)
	}

	switch passthroughRouteListFormat {
	case "table":
		printPassthroughRouteTableFormat(routes, totalCount)
	case "json":
