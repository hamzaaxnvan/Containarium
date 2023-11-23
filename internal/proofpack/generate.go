package proofpack

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

const (
	// SchemaVersion is the ProofPack layout this package writes.
	SchemaVersion = 1
	// LogLimitBytes bounds each step's stored log. The bound is applied in
	// the box, so the daemon never holds more than this per step; the last
	// LogLimitBytes of the output are kept.
	LogLimitBytes = 256 * 1024
	// DefaultRepoDir is where a box's repository is checked out
	// (pkg/core/container.FetchGitSource).
	DefaultRepoDir = "/workspace"
	// DefaultStepTimeout bounds each step.
	DefaultStepTimeout = 10 * time.Minute
	// timeoutKillGraceSecs is how long a timed-out step gets after SIGTERM
	// before SIGKILL.
	timeoutKillGraceSecs = 10
)

// Execer runs a command in a container and reports its exit status. A
// non-zero exit is not an error; err is for failing to run at all.
// incus.Backend satisfies it.
type Execer interface {
	ExecWithExitCode(containerName string, command []string) (stdout, stderr string, exitCode int, err error)
}

// Options configures Generate. Zero values take the defaults.
type Options struct {
	// RepoDir is the repository directory inside the box.
	RepoDir string
	// StepTimeout bounds each step; rounded up to whole seconds.
	StepTimeout time.Duration
	// Now is the clock; tests set it.
	Now func() time.Time
}

// probeScript reports, in one exec, what Generate needs before running
// anything: whether the repo dir exists, whether `timeout` is available,
// the declaration file's content (stdout, bounded), whether a Makefile
// exists and whether `timeout` is missing. A missing `timeout` is only
// reported, not refused here: it matters once a step has to run. Anything at the declaration path counts as present, including a
// dangling symlink or a directory, so an unusable declaration is refused
// (95) rather than mistaken for none. The exit status is 10 + 1
// (declaration present) + 2 (Makefile present) + 4 (`timeout` missing);
// 95 and 97 are the refusals.
const probeScript = `cd -- "$1" || exit 97
rc=10
command -v timeout >/dev/null 2>&1 || rc=$((rc + 4))
if [ -e "$2" ] || [ -L "$2" ]; then head -c "$3" -- "$2" || exit 95; rc=$((rc + 1)); fi
if [ -f Makefile ] || [ -f makefile ] || [ -f GNUmakefile ]; then rc=$((rc + 2)); fi
exit "$rc"`

// stepScript runs one step with its output captured to a temp file in the
// box. The file is opened for writing (fd 3) and reading (fd 4 for the size, fd 5 for the
// tail, each with its own offset) and
// unlinked before the step starts, so a step that deletes or replaces the
