package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var routeDeleteCmd = &cobra.Command{
	Use:   "delete <domain>",
	Short: "Delete a proxy route",
	Long: `Delete a proxy route by domain name.

Examples:
  # Delete a route
  containarium route delete test.example.com --server <host:port>`,
	Aliases: []string{"rm", "remove"},
	Args:    cobra.ExactArgs(1),
	RunE:    runRouteDelete,
}

func init() {
	routeCmd.AddCommand(routeDeleteCmd)
}

func runRouteDelete(cmd *cobra.Command, args []string) error {
	domain := args[0]

	if serverAddr == "" {
		return fmt.Errorf("--server is required")
