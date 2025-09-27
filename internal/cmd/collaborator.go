package cmd

import (
	"github.com/spf13/cobra"
)

// collaboratorCmd represents the collaborator command
var collaboratorCmd = &cobra.Command{
	Use:   "collaborator",
	Short: "Manage container collaborators",
