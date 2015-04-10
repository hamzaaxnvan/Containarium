//go:build !containarium_client

package cmd

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/hosting"
	"github.com/spf13/cobra"
)

var showSecrets bool

var hostingConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "View or manage hosting configuration",
	Long: `View or manage the hosting configuration stored in /etc/containarium/hosting.json.

The configuration file contains:
  - DNS provider name
  - Domain name
  - Email for Let's Encrypt
  - API credentials (masked by default)

Examples:
  # View current configuration
  containarium hosting config

  # View configuration with credentials visible
  sudo containarium hosting config --show-secrets

  # Delete saved configuration
  containarium hosting config --delete`,
	RunE: runHostingConfig,
}

var deleteConfig bool

func init() {
	hostingCmd.AddCommand(hostingConfigCmd)
	hostingConfigCmd.Flags().BoolVar(&showSecrets, "show-secrets", false, "Show API credentials (requires root)")
	hostingConfigCmd.Flags().BoolVar(&deleteConfig, "delete", false, "Delete the saved configuration")
}

func runHostingConfig(cmd *cobra.Command, args []string) error {
	if deleteConfig {
		if err := hosting.DeleteConfig(); err != nil {
			return fmt.Errorf("failed to delete config: %w", err)
		}
		fmt.Printf("Configuration deleted: %s\n", hosting.ConfigPath())
		return nil
	}

	cfg, err := hosting.LoadConfig()
	if err != nil {
