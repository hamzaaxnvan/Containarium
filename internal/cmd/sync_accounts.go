//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"fmt"

	"github.com/footprintai/containarium/internal/collaborator"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/spf13/cobra"
)

var (
	syncDryRun bool
	syncForce  bool
)

var syncOnlyUser string

var syncAccountsCmd = &cobra.Command{
	Use:   "sync-accounts",
	Short: "Sync jump server accounts from persisted containers",
	Long: `Restore jump server SSH accounts by extracting public keys from containers.

This command is useful after the jump server is recreated (e.g., spot instance
termination) when the boot disk is lost but containers persist on the ZFS pool.

It works by:
1. Listing all existing Incus containers
2. Extracting the SSH public key from inside each container
3. Creating the corresponding jump server account with that SSH key

Examples:
  # Sync all jump server accounts from containers
  containarium sync-accounts

  # Dry run - show what would be done without making changes
  containarium sync-accounts --dry-run

  # Force recreation even if accounts exist
  containarium sync-accounts --force`,
	RunE: runSyncAccounts,
}

func init() {
	rootCmd.AddCommand(syncAccountsCmd)

	syncAccountsCmd.Flags().BoolVar(&syncDryRun, "dry-run", false, "Show what would be done without making changes")
	syncAccountsCmd.Flags().BoolVar(&syncForce, "force", false, "Force recreation of accounts even if they exist")
	syncAccountsCmd.Flags().StringVar(&syncOnlyUser, "user", "", "Restore only this container's account. Default (empty) sweeps EVERY container on the host.")
}

func runSyncAccounts(cmd *cobra.Command, args []string) error {
	if verbose {
		fmt.Println("Syncing jump server accounts from persisted containers...")
	}

	// Create container manager
	mgr, err := container.New()
	if err != nil {
		return fmt.Errorf("failed to connect to Incus: %w (is Incus running?)", err)
	}

	// Owner accounts: restore the host jump-server user for every persisted
	// container. The shared helper classifies "no extractable key" (benign —
	// infra/CP/workspace boxes) separately from real create failures, so the
