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
