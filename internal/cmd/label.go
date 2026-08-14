package cmd

import (
	"github.com/spf13/cobra"
)

// labelCmd represents the label command
var labelCmd = &cobra.Command{
	Use:   "label",
	Short: "Manage container labels",
	Long: `Manage Kubernetes-style labels on containers.
