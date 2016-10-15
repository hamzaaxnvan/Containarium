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
	// command no longer fails-closed on the former. See #1010.
	res, err := mgr.SyncOwnerAccounts(container.OwnerSyncOptions{
		Force:    syncForce,
		DryRun:   syncDryRun,
		Verbose:  verbose,
		OnlyUser: syncOnlyUser,
	})
	if err != nil {
		return fmt.Errorf("failed to sync owner accounts: %w", err)
	}

	// --user naming a container that does not exist must not look like success.
	// The sweep would report "0 restored, N skipped" and exit 0, which reads as
	// "nothing needed doing" when it actually means "you typo'd the box name".
	if syncOnlyUser != "" && len(res.Restored) == 0 && len(res.NoKey) == 0 && len(res.Failed) == 0 {
		if !container.UserExists(syncOnlyUser) {
			return fmt.Errorf("no container found for --user %q (expected an incus container named %q)",
				syncOnlyUser, syncOnlyUser+"-container")
		}
		fmt.Printf("  Account for %s already exists and is up to date (use --force to recreate)\n", syncOnlyUser)
	}
	for _, u := range res.Restored {
		// Always print the key COUNT. A box whose container holds three keys
		// coming back with one is the #1477 truncation, and without this line
		// it stays invisible until somebody's login is refused.
		keys := res.KeysRestored[u]
		if syncDryRun {
			fmt.Printf("  [DRY RUN] Would create jump server account for %s (%s)\n", u, pluralKeys(keys))
		} else {
			fmt.Printf("  ✓ Jump server account restored for %s (%s)\n", u, pluralKeys(keys))
		}
	}
	for _, u := range res.Partial {
		fmt.Printf("  ! Partial restore for %s — the account works, but at least one additional key could not be authorized\n", u)
	}
	for _, u := range res.NoKey {
		fmt.Printf("  – No SSH key in container %s-container (infra/CP/workspace box?) — skipped\n", u)
	}
	for _, u := range res.Failed {
		fmt.Printf("  ✗ Failed to create jump server account for %s\n", u)
	}

	// Sync collaborator accounts from PostgreSQL
	collabRestored, collabSkipped, collabFailed := syncCollaboratorAccounts(mgr)

	// Print summary
	fmt.Println()
	fmt.Println("==========================================")
	fmt.Println("Jump Server Account Sync Summary")
	fmt.Println("==========================================")
	fmt.Println("Owner Accounts:")
	if syncDryRun {
		fmt.Printf("  Would restore: %d accounts\n", len(res.Restored))
	} else {
		fmt.Printf("  Restored:      %d accounts\n", len(res.Restored))
	}
	fmt.Printf("  Skipped:       %d accounts\n", res.Skipped)
	if len(res.Partial) > 0 {
		fmt.Printf("  Partial:       %d accounts (created, but not every key authorized)\n", len(res.Partial))
	}
	fmt.Printf("  No key (ok):   %d accounts\n", len(res.NoKey))
	fmt.Printf("  Failed:        %d accounts\n", len(res.Failed))
	fmt.Println()
	fmt.Println("Collaborator Accounts:")
	if syncDryRun {
		fmt.Printf("  Would restore: %d accounts\n", collabRestored)
	} else {
