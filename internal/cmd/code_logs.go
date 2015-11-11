package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/internal/runlog"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// `containarium code runs|logs` (#2123) read a box's code runs through the
// daemon API (ListBoxRuns / TailBoxRunLog) instead of SSH: the same reads a
// browser or phone makes. `code attach` keeps its SSH path.

var codeLogsFollow bool

var codeRunsCmd = &cobra.Command{
	Use:   "runs <box>",
	Short: "List a box's code runs through the daemon API",
	Long: `List the code runs on a box (containarium code run): name, outcome
(running, exited, or unknown when the run was killed or the box restarted),
exit code, start and end time, and the log path inside the box.

Reads the box's run records through the daemon API, so it needs no SSH access
of its own — only a token that may connect to the box (its owner or a
collaborator, with the ssh:write scope).

Examples:
  containarium code runs alice --server <host>
  containarium code runs alice --server <host> --http`,
	Args: cobra.ExactArgs(1),
	RunE: runCodeRuns,
}

var codeLogsCmd = &cobra.Command{
	Use:   "logs <box> <run>",
	Short: "Print a box code run's output through the daemon API",
	Long: `Print a code run's output from its log on the box.

--follow keeps reading until the run has exited; a dropped connection loses
nothing, since every read resumes at the byte offset the previous one ended at.
For a run started with stream-json output (a framed log) only stdout is
printed; a note on stderr says when the run's stderr was left out — read it
with ` + "`containarium code attach`" + `.

Examples:
  containarium code logs alice code --server <host>
  containarium code logs alice code --follow --server <host>`,
	Args: cobra.ExactArgs(2),
	RunE: runCodeLogs,
}

func init() {
	codeCmd.AddCommand(codeRunsCmd)
	codeCmd.AddCommand(codeLogsCmd)
	codeLogsCmd.Flags().BoolVarP(&codeLogsFollow, "follow", "f", false, "Keep reading until the run exits")
}

// boxRunsAPI is what code runs/logs need from a daemon client.
