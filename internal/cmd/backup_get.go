package cmd

import (
	"fmt"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var backupGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show a single backup's metadata",
	Args:  cobra.ExactArgs(1),
	RunE:  runBackupGet,
}

func init() {
	backupCmd.AddCommand(backupGetCmd)
