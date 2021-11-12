//go:build !containarium_client

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/spf13/cobra"
)

var (
	exportOutputPath string
	exportKeyPath    string
	jumpServerIP     string
)

var exportCmd = &cobra.Command{
	Use:   "export <username>",
	Short: "Export SSH configuration for a user",
	Long: `Export SSH configuration snippet for accessing a user's container via ProxyJump.

The generated configuration can be added to your ~/.ssh/config file or saved to ~/.ssh/config.d/
for automatic inclusion.

Examples:
  # Export to stdout
  containarium export alice

  # Export to specific file
  containarium export alice --output ~/.ssh/config.d/containarium-alice

  # Specify SSH key path
  containarium export alice --key ~/.ssh/containarium_alice

  # Specify jump server IP
  containarium export alice --jump-ip 35.229.246.67`,
	Args: cobra.ExactArgs(1),
	RunE: runExport,
}

func init() {
	rootCmd.AddCommand(exportCmd)

	exportCmd.Flags().StringVarP(&exportOutputPath, "output", "o", "", "Output file path (default: stdout)")
	exportCmd.Flags().StringVar(&exportKeyPath, "key", "~/.ssh/id_rsa", "SSH private key path")
	exportCmd.Flags().StringVar(&jumpServerIP, "jump-ip", "", "Jump server IP address (required for export)")
	_ = exportCmd.MarkFlagRequired("jump-ip")
}

