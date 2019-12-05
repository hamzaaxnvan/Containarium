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
