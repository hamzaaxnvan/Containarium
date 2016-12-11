package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/ostype"
	"github.com/spf13/cobra"
)

var (
	outputFormat string
	filterState  string
	filterUser   string
	filterLabels []string
	showLabels   bool
	groupByLabel string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all containers",
	Long: `List all LXC containers with their current state, resources, and network information.

Examples:
  # List all containers
  containarium list

  # List only running containers
  containarium list --state running

  # List in JSON format
  containarium list --format json

  # List containers for a specific user
  containarium list --user alice

  # Filter by labels
  containarium list --label team=dev --label project=web

  # Show labels in table output
  containarium list --show-labels

  # Group containers by a label
  containarium list --group-by team

  # Group by label and show all labels
  containarium list --group-by env --show-labels`,
	Aliases: []string{"ls"},
	RunE:    runList,
}

func init() {
	rootCmd.AddCommand(listCmd)

	listCmd.Flags().StringVarP(&outputFormat, "format", "f", "table", "Output format: table, json, yaml")
	listCmd.Flags().StringVar(&filterState, "state", "all", "Filter by state: all, running, stopped")
	listCmd.Flags().StringVar(&filterUser, "user", "", "Filter by username")
	listCmd.Flags().StringSliceVarP(&filterLabels, "label", "l", []string{}, "Filter by label (key=value format, can be specified multiple times)")
	listCmd.Flags().BoolVar(&showLabels, "show-labels", false, "Show labels in table output")
	listCmd.Flags().StringVarP(&groupByLabel, "group-by", "g", "", "Group containers by a label key (e.g., --group-by team)")
}

func runList(cmd *cobra.Command, args []string) error {
	var containers []incus.ContainerInfo
	var err error

	// Check if using remote server
	if httpMode && serverAddr != "" {
		// Use HTTP client for remote server
		containers, err = listRemoteHTTP()
		if err != nil {
			return fmt.Errorf("failed to list containers from HTTP API: %w", err)
		}
	} else if serverAddr != "" {
		// Use gRPC client for remote server
		containers, err = listRemote()
		if err != nil {
			return fmt.Errorf("failed to list containers from remote server: %w", err)
		}
	} else {
		// Use local Incus connection
		containers, err = listLocal()
		if err != nil {
			return fmt.Errorf("failed to list containers: %w", err)
		}
	}

	// Parse label filters
	labelFilter := parseLabelFilter(filterLabels)

	// Apply filters
	var filtered []incus.ContainerInfo
	runningCount := 0
	stoppedCount := 0

	for _, c := range containers {
		// Filter by user
		if filterUser != "" {
			// Extract username from container name (format: username-container)
			username := strings.TrimSuffix(c.Name, "-container")
			if username != filterUser {
				continue
			}
		}

		// Filter by state
		if filterState != "all" {
			if filterState == "running" && c.State != "Running" {
