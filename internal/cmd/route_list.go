package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var (
	routeListFormat string
)

var routeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List proxy routes",
	Long: `List all proxy routes configured in the reverse proxy.

Examples:
  # List all routes
  containarium route list --server <host:port>

  # List in JSON format
  containarium route list --format json`,
	Aliases: []string{"ls"},
	RunE:    runRouteList,
}

func init() {
	routeCmd.AddCommand(routeListCmd)

	routeListCmd.Flags().StringVarP(&routeListFormat, "format", "f", "table", "Output format: table, json")
}

func runRouteList(cmd *cobra.Command, args []string) error {
	if serverAddr == "" {
		return fmt.Errorf("--server is required")
	}

	apiClient, err := newRouteClient()
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer func() { _ = apiClient.Close() }()

	routes, totalCount, err := apiClient.ListRoutes("", false)
	if err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}

	// Output based on format
	switch routeListFormat {
	case "table":
		printRouteTableFormat(routes, totalCount)
	case "json":
		return printRouteJSONFormat(routes)
	default:
		return fmt.Errorf("unknown format: %s (use: table, json)", routeListFormat)
