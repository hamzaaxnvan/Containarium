package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var labelListFormat string

var labelListCmd = &cobra.Command{
	Use:   "list <username>",
	Short: "List labels for a container",
	Long: `List all labels for a specific container.

Shows all Kubernetes-style labels attached to the container.

Examples:
  # List labels for a container
  containarium label list alice

  # List labels in JSON format
  containarium label list alice --format json`,
	Aliases: []string{"ls", "get"},
	Args:    cobra.ExactArgs(1),
	RunE:    runLabelList,
}

func init() {
