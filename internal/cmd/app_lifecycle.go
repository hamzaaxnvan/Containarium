package cmd

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var (
	lifecycleUsername string
)

// Stop command
var appStopCmd = &cobra.Command{
	Use:   "stop <app-name>",
	Short: "Stop a running application",
	Long: `Stop a running application.

The application can be started again later using 'containarium app start'.

Examples:
  containarium app stop myapp --server <host:port> --user alice`,
	Args: cobra.ExactArgs(1),
	RunE: runAppStop,
}

// Start command
var appStartCmd = &cobra.Command{
	Use:   "start <app-name>",
	Short: "Start a stopped application",
	Long: `Start a previously stopped application.

Examples:
  containarium app start myapp --server <host:port> --user alice`,
	Args: cobra.ExactArgs(1),
	RunE: runAppStart,
}

// Restart command
var appRestartCmd = &cobra.Command{
	Use:   "restart <app-name>",
	Short: "Restart an application",
	Long: `Restart an application by stopping and starting it.

Examples:
  containarium app restart myapp --server <host:port> --user alice`,
	Args: cobra.ExactArgs(1),
