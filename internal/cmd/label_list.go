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
	labelCmd.AddCommand(labelListCmd)
	labelListCmd.Flags().StringVarP(&labelListFormat, "format", "f", "table", "Output format: table, json")
}

func runLabelList(cmd *cobra.Command, args []string) error {
	username := args[0]

	// Get labels - use remote or local mode
	var labels map[string]string
	var err error

	if httpMode && serverAddr != "" {
		labels, err = getLabelsRemoteHTTP(username)
	} else if serverAddr != "" {
		return fmt.Errorf("label operations not yet supported via gRPC - use --http mode")
	} else {
		labels, err = getLabelsLocal(username)
	}

	if err != nil {
		return err
	}

	// Output based on format
	switch labelListFormat {
	case "json":
		return printLabelsJSON(username, labels)
	case "table":
		printLabelsTable(username, labels)
	default:
		return fmt.Errorf("unknown format: %s (use: table, json)", labelListFormat)
	}

	return nil
