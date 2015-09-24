package cmd

import (
	"github.com/spf13/cobra"
)

// collaboratorCmd represents the collaborator command
var collaboratorCmd = &cobra.Command{
	Use:   "collaborator",
	Short: "Manage container collaborators",
	Long: `Manage collaborators who can access a container.

Collaborators can SSH into a container via their own account and then
switch to the container owner's account using 'sudo su'. All sessions
are logged for auditing purposes.

Examples:
  # Add a collaborator to alice's container
  containarium collaborator add alice bob --ssh-key ~/.ssh/bob.pub

