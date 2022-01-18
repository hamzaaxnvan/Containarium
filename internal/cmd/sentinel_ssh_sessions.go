//go:build !windows && !containarium_client

// `containarium sentinel ssh-sessions list`/`follow` — the CLI subcommand
// containarium#1980's own acceptance criteria called for but did not ship
// (split out as containarium#2004 to keep #1980's PR reviewable).
//
// Both subcommands read internal/sentinel/sshsession.Record directly off
// the local JSONL sink `containarium sentinel ssh-session-plugin` appends
// to (internal/cmd/sentinel_ssh_session_plugin.go) — no proto, no server
// round trip, since the sink is a local file the CLI and the plugin
// process both run against on the same host (see that command's own doc
// comment for why). This command is entirely read-only: it never opens
// the sink for writing and never touches the plugin process itself.
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	"github.com/spf13/cobra"
)

var (
	sshSessionsFile      string
	sshSessionsSessionID string
	sshSessionsLogin     string
	sshSessionsJSON      bool
)

var sentinelSSHSessionsCmd = &cobra.Command{
	Use:   "ssh-sessions",
	Short: "List or follow structured SSH session lifecycle records (containarium#1980)",
	Long: `Reads the JSONL sink that "containarium sentinel ssh-session-plugin" (containarium#1980)
appends one record to per SSH session open/close event on this sentinel.

This command only reads that file locally — it never talks to sshpiperd or
the plugin process — so it works equally well against a live sink or a
copy of one pulled off a sentinel for offline review.`,
}

var sentinelSSHSessionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SSH session records, newest first",
	Long: `List SSH session lifecycle records from the sink, newest first.

Examples:
  containarium sentinel ssh-sessions list
  containarium sentinel ssh-sessions list --login alice
  containarium sentinel ssh-sessions list --session-id 3f2c9a1e-...  --json
  containarium sentinel ssh-sessions list --records-file /path/to/ssh-sessions.jsonl`,
	Args: cobra.NoArgs,
	RunE: runSentinelSSHSessionsList,
}

var sentinelSSHSessionsFollowCmd = &cobra.Command{
	Use:   "follow",
	Short: "Follow the sink for new SSH session records, like tail -f",
	Long: `Follow the sink for SSH session lifecycle records as they are appended,
similar to tail -f. Starts from the current end of the file (existing
history is NOT replayed) — pair with "list" first if you also want that.

Runs until interrupted (Ctrl-C / SIGTERM).

Examples:
  containarium sentinel ssh-sessions follow
  containarium sentinel ssh-sessions follow --login alice
  containarium sentinel ssh-sessions follow --json`,
	Args: cobra.NoArgs,
	RunE: runSentinelSSHSessionsFollow,
