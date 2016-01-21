package cmd

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var labelOverwrite bool

var labelSetCmd = &cobra.Command{
	Use:   "set <username> <key=value> [key=value...]",
	Short: "Set labels on a container",
	Long: `Set one or more labels on a container.

Labels are specified as key=value pairs. Multiple labels can be set at once.
By default, existing labels with the same key will be overwritten.

Examples:
  # Set a single label
  containarium label set alice team=backend

  # Set multiple labels at once
  containarium label set alice team=backend project=api env=production

  # Overwrite existing labels
  containarium label set alice team=frontend`,
	Args: cobra.MinimumNArgs(2),
	RunE: runLabelSet,
}

func init() {
	labelCmd.AddCommand(labelSetCmd)
	labelSetCmd.Flags().BoolVar(&labelOverwrite, "overwrite", true, "Overwrite existing labels with the same key")
}

func runLabelSet(cmd *cobra.Command, args []string) error {
	username := args[0]
	labelArgs := args[1:]

	// Parse labels from key=value format
	labels := make(map[string]string)
	for _, arg := range labelArgs {
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid label format: %q (expected key=value)", arg)
