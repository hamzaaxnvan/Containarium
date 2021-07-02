package cmd

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/internal/toolaccess"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// The tool-access verbs record, per box, what a platform-started (unattended,
// headless) agent run may do (#2944), via the SetContainerToolAccess RPC
// (POST /v1/containers/{name}/tool-access), which stamps
// user.containarium.tool_access on the Incus container. Only platform-started
// runs are governed; the owner's own login and `containarium code run` over SSH
// are unaffected. The level is read back from the container view (like
// `ttl get`), so there is no separate get RPC.
//
// This change only RECORDS the level; applying it to the run path lands
// separately, so setting one has no effect on a run yet.

var toolAccessCmd = &cobra.Command{
	Use:   "tool-access",
	Short: "Record the tool-access level for platform-started runs on a box",
}

var toolAccessSetCmd = &cobra.Command{
	Use:   "set <username> <none|build-tools|full-sudo>",
	Short: "Record a box's tool-access level",
	Long: `Record what a platform-started (unattended) agent run on this box may do (#2944):

  none         nothing that needs approval
  build-tools  the platform-managed build and test toolchains
  full-sudo    passwordless sudo for the unprivileged run user (an explicit,
               per-box choice; software installed this way is the owner's to maintain)

Only platform-started runs are governed; the owner's own login is unaffected.
Use 'containarium tool-access clear' to remove a recorded level.

Examples:
  containarium tool-access set alice build-tools
  containarium tool-access get alice`,
	Args: cobra.ExactArgs(2),
	RunE: runToolAccessSet,
}

var toolAccessClearCmd = &cobra.Command{
	Use:   "clear <username>",
	Short: "Remove a box's recorded tool-access level",
	Args:  cobra.ExactArgs(1),
	RunE:  runToolAccessClear,
}

var toolAccessGetCmd = &cobra.Command{
	Use:   "get <username>",
	Short: "Show a box's recorded tool-access level",
	Args:  cobra.ExactArgs(1),
	RunE:  runToolAccessGet,
}

var toolAccessRunUserCmd = &cobra.Command{
	Use:   "run-user <username>",
	Short: "Show whether a box's run user is signed in",
	Long: `Show whether the unprivileged run user (#2944) on a box has a usable agent
sign-in: the same check 'StartBoxRun' applies before it refuses a platform run,
without starting one (#3274). Prints the credential source's name only, never a
value, and does not create the run user.

States: interactive and api-key mean a run would proceed; none means the run
user has never signed in (or does not exist yet); expired means it must sign in
again. A box with no tool-access level recorded runs as its owner, so there is
no run user to check.

Examples:
  containarium tool-access run-user alice`,
	Args: cobra.ExactArgs(1),
	RunE: runToolAccessRunUser,
}

func init() {
	toolAccessCmd.AddCommand(toolAccessSetCmd, toolAccessClearCmd, toolAccessGetCmd, toolAccessRunUserCmd)
	rootCmd.AddCommand(toolAccessCmd)
}

// parseToolAccess maps a CLI level name to the proto enum. UNSPECIFIED is not
// a level a caller names; clearing has its own verb.
func parseToolAccess(s string) (pb.BoxToolAccess, error) {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "_", "-")) {
	case "none":
		return pb.BoxToolAccess_BOX_TOOL_ACCESS_NONE, nil
	case "build-tools":
		return pb.BoxToolAccess_BOX_TOOL_ACCESS_BUILD_TOOLS, nil
	case "full-sudo":
		return pb.BoxToolAccess_BOX_TOOL_ACCESS_FULL_SUDO, nil
	}
	return pb.BoxToolAccess_BOX_TOOL_ACCESS_UNSPECIFIED, fmt.Errorf("unknown level %q (want none, build-tools or full-sudo)", s)
}

// toolAccessName is the CLI spelling of a level, "(none recorded)" for UNSPECIFIED.
func toolAccessName(a pb.BoxToolAccess) string {
	switch a {
	case pb.BoxToolAccess_BOX_TOOL_ACCESS_NONE:
		return "none"
	case pb.BoxToolAccess_BOX_TOOL_ACCESS_BUILD_TOOLS:
		return "build-tools"
	case pb.BoxToolAccess_BOX_TOOL_ACCESS_FULL_SUDO:
		return "full-sudo"
	}
	return "(none recorded)"
}

func runToolAccessSet(cmd *cobra.Command, args []string) error {
	level, err := parseToolAccess(args[1])
	if err != nil {
		return err
	}
	return setToolAccess(args[0], level, fmt.Sprintf("✓ %s: tool access recorded as %s (for platform-started runs only)\n", args[0], toolAccessName(level)))
