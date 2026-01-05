package cmd

import (
	"fmt"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var (
	backupPruneDatabase string
	backupPruneKeep     int32
)

var backupPruneCmd = &cobra.Command{
	Use:   "prune <username>",
	Short: "Delete older backups, keeping only the newest N per database",
	Long: `Delete older backup records for a tenant, keeping the newest --keep per
database (or per hook label, for a --hook backup, which fills the same
slot). One database's history never counts against another's: omit
--database to prune every database this tenant has backups for, each
independently down to --keep.

This is the missing half of a scheduled backup (#1831, #1836): a
scheduler that only ever creates and never prunes will fill the backup
directory or object-store bucket without bound. Run this on whatever
cadence you like — the same cadence as backup create, or slower — it
doesn't need to be coupled to it.

A record whose delete fails (e.g. a transient object-store error) is
reported, not fatal: everything else that should be pruned still is.

Examples:
  containarium backup prune alice --database app --keep 7 --server <host>
  containarium backup prune alice --keep 3 --server <host>   # every database, keep 3 each`,
	Args: cobra.ExactArgs(1),
	RunE: runBackupPrune,
}

func init() {
	backupCmd.AddCommand(backupPruneCmd)
