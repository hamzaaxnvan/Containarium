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
}

func runBackupGet(cmd *cobra.Command, args []string) error {
	c, err := newBackupClientFn()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	r, err := c.GetBackup(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("ID:          %s\n", r.Id)
	fmt.Printf("User:        %s\n", r.Username)
	fmt.Printf("Database:    %s\n", r.Database)
	fmt.Printf("Engine:      %s\n", engineLabel(r.Engine))
	fmt.Printf("Created:     %s\n", r.CreatedAt)
	fmt.Printf("Size:        %s\n", humanBytes(r.SizeBytes))
