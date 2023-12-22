package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var backupDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a stored dump and its index entry",
	Long: `Delete a stored dump (and its metadata sidecar) by id. To keep only
