package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var labelRemoveCmd = &cobra.Command{
	Use:   "remove <username> <key> [key...]",
	Short: "Remove labels from a container",
	Long: `Remove one or more labels from a container.

Specify the label keys to remove. Non-existent labels are silently ignored.

Examples:
  # Remove a single label
  containarium label remove alice env

  # Remove multiple labels at once
  containarium label remove alice env project team`,
	Aliases: []string{"rm", "delete"},
	Args:    cobra.MinimumNArgs(2),
	RunE:    runLabelRemove,
}

func init() {
	labelCmd.AddCommand(labelRemoveCmd)
}

func runLabelRemove(cmd *cobra.Command, args []string) error {
	username := args[0]
	keys := args[1:]

	// Remove labels - use remote or local mode
	if httpMode && serverAddr != "" {
		return removeLabelsRemoteHTTP(username, keys)
	} else if serverAddr != "" {
		return fmt.Errorf("label operations not yet supported via gRPC - use --http mode")
