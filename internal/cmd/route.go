package cmd

import (
	"github.com/spf13/cobra"
)

// routeCmd represents the route command
var routeCmd = &cobra.Command{
	Use:   "route",
	Short: "Manage proxy routes (domain to container mappings)",
	Long: `Manage proxy routes in Containarium.

Routes map external domains to internal container IPs, enabling external access
to services running in containers.
