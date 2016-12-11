package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var debugFormat string

var debugCmd = &cobra.Command{
	Use:   "debug <username>",
	Short: "Diagnose a container's SSH path",
	Long: `Inspect backend-local state for a container to explain SSH failures.

Returns a structured report covering:
  - container runtime state (running / stopped / missing)
  - host /etc/passwd entry for the username
  - whether the user's shell wrapper exists on the backend
  - recent sshd journal lines mentioning this user
  - a likely_cause + ordered next_actions list

Use --format json to get a machine-readable report. The same diagnostic is
exposed to AI agents as the debug_container MCP tool.

Examples:
  containarium debug alice
  containarium debug alice --format json`,
	Args: cobra.ExactArgs(1),
	RunE: runDebug,
}

func init() {
	rootCmd.AddCommand(debugCmd)
	debugCmd.Flags().StringVarP(&debugFormat, "format", "f", "human", "Output format: human | json")
}

func runDebug(cmd *cobra.Command, args []string) error {
	username := args[0]
	// debug reads a box's host-level diagnostics (sshd journal, shell); the
