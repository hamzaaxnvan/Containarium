package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// Tracker dispatch (#2022): turn routed `scope:<role>` labels into runs.
// Each tick is one DispatchTrackerIssues call — the daemon does the work
// and guarantees exactly one run per issue across restarts and
// concurrent dispatchers. This command is only the loop around it.
// Requires tracker:admin and agents:run. Not importing internal/tracker
// here is deliberate: it links pgx, which the client build must not
// (scripts/test-cli-split-deps.sh).

var trackerDispatchCmd = &cobra.Command{
	Use:   "dispatch <username> <connection> [--once | --interval 60s]",
	Short: "Start the routed skill for issues labeled scope:<role>, exactly once each",
	Long: `Poll a tracker connection and, for every open issue carrying a routed
scope:<role> label and no agent:* state label (and not gated with
agent:needs-approval), start the routed skill and label the issue
agent:queued. The run gets the issue reference, never its body.

An issue is dispatched at most once while a run for it is queued or
running, even across restarts or several dispatchers. To re-run a
finished issue, remove agent:done / agent:failed and re-add the scope
label. A scope:* label with no route gets one warning comment.

  containarium tracker dispatch alice default --once
  containarium tracker dispatch alice default --interval 60s
  containarium tracker dispatch alice default --once --max-starts 2

--max-starts N stops each tick once N runs have started (failed starts
and skipped issues do not count). The eligible issues past the limit get
no dispatch row and no label, so a later tick picks them up; the tick
line reports them as left-undispatched.

Requires tracker:admin and agents:run.`,
	Args: cobra.ExactArgs(2),
	RunE: runTrackerDispatch,
}

var trackerDispatchesCmd = &cobra.Command{
	Use:   "dispatches <username> <connection> [--state queued|running|done|failed]",
	Short: "List a connection's dispatches, newest first",
	Args:  cobra.ExactArgs(2),
	RunE:  runTrackerDispatches,
}

const defaultDispatchInterval = 60 * time.Second

var (
	trackerDispatchOnce     bool
	trackerDispatchInterval time.Duration
	trackerDispatchesState  string
	// trackerDispatchMaxStarts bounds the runs each tick starts (#2270);
	// 0 is unlimited.
