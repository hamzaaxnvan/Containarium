package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var collaboratorRemoveCmd = &cobra.Command{
	Use:   "remove <owner-username> <collaborator-username>",
	Short: "Remove a collaborator from a container",
	Long: `Remove a collaborator from a container.

This will:
1. Remove the collaborator's user account from the container
2. Remove the collaborator's jump server account
3. Remove the collaborator from the database

