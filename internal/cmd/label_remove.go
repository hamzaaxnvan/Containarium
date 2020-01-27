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

