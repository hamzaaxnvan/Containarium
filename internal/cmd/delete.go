package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var (
	forceDelete bool
)

var deleteCmd = &cobra.Command{
	Use:   "delete <username>",
	Short: "Delete a container",
	Long: `Delete a user's LXC container.

By default, the container must be stopped before deletion.
Use --force to delete a running container.

Examples:
  # Delete a stopped container
  containarium delete alice

  # Force delete a running container
  containarium delete bob --force`,
	Args:    cobra.ExactArgs(1),
	Aliases: []string{"rm", "remove"},
	RunE:    runDelete,
}

func init() {
	rootCmd.AddCommand(deleteCmd)

	deleteCmd.Flags().BoolVarP(&forceDelete, "force", "f", false, "Force delete even if container is running")
}

func runDelete(cmd *cobra.Command, args []string) error {
	return deleteBox(args[0], forceDelete)
}

// deleteBox deletes the container for username, remote or local, and (local
// mode only) the jump-server account. Shared by `delete` and `box delete`.
func deleteBox(username string, force bool) error {
	containerName := username + "-container"

	if verbose {
		fmt.Printf("Deleting container: %s\n", containerName)
		if force {
			fmt.Println("Force delete enabled")
