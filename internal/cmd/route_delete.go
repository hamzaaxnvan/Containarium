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
