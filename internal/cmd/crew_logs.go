package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/footprintai/containarium/internal/runlog"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var (
	crewLogsSkill  string
	crewLogsFollow bool
)

var crewLogsCmd = &cobra.Command{
	Use:   "logs <run-id>",
	Short: "Print a run's journal (crew or single-skill run)",
	Long: `Print the journal a run's agent writes on its member box: status, assistant,
tool_use, tool_result and error lines, one JSON object per line.

--skill picks the member (default: the only member of a skill run, or the
crew's entry skill). --follow keeps reading until the journal's "run ended"
line; a dropped connection loses nothing, since every read resumes at the
byte offset the previous one ended at.

Examples:
  containarium crew logs <run-id> --server <host>
  containarium crew logs <run-id> --skill hello-agent --follow --server <host>`,
	Args: cobra.ExactArgs(1),
	RunE: runCrewLogs,
}

