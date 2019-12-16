package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var (
	logsUsername  string
	logsTailLines int32
	logsFollow    bool
)

var appLogsCmd = &cobra.Command{
	Use:   "logs <app-name>",
	Short: "Get application logs",
	Long: `Display logs from a deployed application.

Examples:
  # View last 100 lines (default)
  containarium app logs myapp --server <host:port> --user alice

  # View last 500 lines
  containarium app logs myapp --tail 500

  # Follow logs in real-time (coming soon)
  containarium app logs myapp --follow`,
	Args: cobra.ExactArgs(1),
	RunE: runAppLogs,
}

func init() {
	appCmd.AddCommand(appLogsCmd)

	appLogsCmd.Flags().StringVarP(&logsUsername, "user", "u", "", "Username (required)")
	appLogsCmd.Flags().Int32VarP(&logsTailLines, "tail", "t", 100, "Number of lines to show from the end")
	appLogsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output (coming soon)")
}

