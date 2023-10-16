package server

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/footprintai/containarium/internal/threatdetect"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// Host-side kill for a miner-signature match (#2638; design component 7,
// the kill_process row): re-read /proc/<pid>/cgroup, confirm the pid still
// belongs to the same container, then SIGKILL it. The pid comes from an exec
// event that may be seconds old; between the exec and the kill the process
// can exit and its pid be reused by an unrelated process, on the host or in
// another tenant's box. The cgroup re-check is what makes that reuse a
// skipped kill rather than a signal to a stranger; the start-time check
// (#2765) does the same for a reuse inside the same box: the pinned process
// must not have started after the exec watcher stamped the matched exec
// (exec_stamp.go). The kill is Incus-only (#2768): on another runtime it is
// refused up front.

var (
	// errKillPIDReused: the pid is no longer the matched process: it now
	// lives in a different cgroup than the container's, or it started after
	// the matched exec was stamped (reused inside the same box, #2765). The
	// wrapping error says which.
	errKillPIDReused = errors.New("pid reused")
	// errKillProcessGone: the process exited before the signal.
	errKillProcessGone = errors.New("process already exited")
	// errKillContainerInit: the pid is the container's init. Signalling it
	// from the host would stop the whole box; the policy action (freeze,
	// throttle) is the tool for that, not the kill.
	errKillContainerInit = errors.New("pid is the container's init")
	// errKillStartTimeUnknown (#2765): the matched exec carries no stamp (the
	// watcher could not read the boot clock), or the pinned pid's start time
	// cannot be read or parsed, so whatever holds the pid cannot be confirmed
	// as the matched process. Fail closed: no signal.
	errKillStartTimeUnknown = errors.New("start time unknown: the pid cannot be confirmed as the matched process")
	// errKillRuntimeUnsupported: the container's init has no Incus payload
	// cgroup, so there is no container root to confirm the pid against
	// (#2768, decided on #2781). Refused before anything else is checked,
	// instead of every kill failing the cgroup compare as "pid reused".
	errKillRuntimeUnsupported = errors.New("kill_process is Incus-only: the container's init has no Incus payload cgroup")
)

// processKiller is what the responder needs for kill_process. An interface
// so the responder is unit-testable without signalling real processes.
type processKiller interface {
	// KillInContainer SIGKILLs host pid if, and only if, it is still a
	// process of container that started no later than stamp (the boot-clock
	// stamp the exec watcher put on the matched exec, in /proc/<pid>/stat
	// field 22 units, #2765). errKillPIDReused,
	// errKillProcessGone, errKillContainerInit, errKillStartTimeUnknown or
	// errKillRuntimeUnsupported (wrapped) when it refuses.
	KillInContainer(container string, pid uint32, stamp threatdetect.ProcStartTicks) error
}

// pidHandle pins one process for the check-then-signal sequence. On Linux it
// is a pidfd, so a pid reused after the handle was opened can never receive
// the signal; see process_kill_linux.go.
type pidHandle interface {
	Kill() error
	Close() error
}

// hostProcessKiller is the real processKiller.
type hostProcessKiller struct {
	procRoot string
	// initPID returns the container's init pid on the host (0 = not running).
	initPID func(container string) (int64, error)
