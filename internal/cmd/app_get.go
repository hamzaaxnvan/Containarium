package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var (
	getUsername string
	getFormat   string
)

var appGetCmd = &cobra.Command{
	Use:   "get <app-name>",
	Short: "Get application details",
	Long: `Display detailed information about a deployed application.

Examples:
  # Get app details
  containarium app get myapp --server <host:port> --user alice

  # Get in JSON format
  containarium app get myapp --format json`,
	Args: cobra.ExactArgs(1),
	RunE: runAppGet,
}

func init() {
	appCmd.AddCommand(appGetCmd)

	appGetCmd.Flags().StringVarP(&getUsername, "user", "u", "", "Username (required)")
	appGetCmd.Flags().StringVarP(&getFormat, "format", "f", "table", "Output format: table, json")
}

func runAppGet(cmd *cobra.Command, args []string) error {
	appName := args[0]

	if serverAddr == "" {
		return fmt.Errorf("--server is required")
	}

	if getUsername == "" {
		return fmt.Errorf("--user is required")
	}

	grpcClient, err := client.NewGRPCClient(serverAddr, certsDir, insecure)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer func() { _ = grpcClient.Close() }()

	app, err := grpcClient.GetApp(getUsername, appName)
	if err != nil {
		return fmt.Errorf("failed to get app: %w", err)
	}

	switch getFormat {
	case "table":
