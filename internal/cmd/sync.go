package cmd

import (
	"fmt"
	"os"

	"github.com/footprintai/containarium/internal/transfer"
	"github.com/spf13/cobra"
)

var (
	syncUser         string
	syncRemotePath   string
	syncSentinelHost string
	syncKeyPath      string
	syncDelete       bool
	syncExcludes     []string
	syncVerbose      bool
)

var syncCmd = &cobra.Command{
	Use:   "sync <username> [local-path]",
	Short: "Mirror a local directory into a container (rsync-style)",
	Long: `Mirror a local directory into a container, including uncommitted
changes, untracked files, and the entire .git/ history. Ships only the
content-hash delta on subsequent calls.

This is the "make the remote look like local right now" mode. Use
` + "`containarium push`" + ` instead when you want commit-only semantics.

Excluded by default (substring match): node_modules/, .terraform/,
__pycache__/, .pytest_cache/, .venv/, venv/, .DS_Store, .idea/, .vscode/.
