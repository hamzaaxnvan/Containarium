package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/spf13/cobra"
)

var (
	pruneState        string
	pruneNameContains string
	pruneOlderThan    string
	pruneLabels       []string
	pruneDryRun       bool
	pruneYes          bool
	pruneForce        bool
)

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Bulk-delete containers matching a filter (fleet cleanup)",
	Long: `Delete every container matching a filter in one command, instead of
deleting them one by one.

Built for fleet hygiene: reaping a pile of leaked/finished ephemeral boxes
(e.g. CI debug boxes that outlived their run). It LISTs containers, narrows by
the filters you pass, shows exactly which boxes match, and — after you confirm —
deletes each. Core platform containers are never eligible.

At least one filter is required (refusing to prune everything by accident).
Filters combine with AND. By default you're prompted to confirm; use --yes to
skip the prompt (e.g. in scripts) or --dry-run to only preview.

Examples:
  # Preview which stopped boxes would be deleted (no deletion)
  containarium prune --state stopped --dry-run

  # Delete all stopped boxes created over an hour ago, after confirming
  containarium prune --state stopped --older-than 1h

  # Reap all CI boxes by label, no prompt (scripted)
  containarium prune --label managed_by=ci --yes

  # Delete boxes whose name contains "pr-" older than a day
  containarium prune --name-contains pr- --older-than 24h --yes`,
	Args: cobra.NoArgs,
	RunE: runPrune,
}

func init() {
	rootCmd.AddCommand(pruneCmd)
	pruneCmd.Flags().StringVar(&pruneState, "state", "", "Only containers in this state: running | stopped")
