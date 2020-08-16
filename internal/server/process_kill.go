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
	// open pins pid; errKillProcessGone when it no longer exists.
	open func(pid int) (pidHandle, error)
}

func newHostProcessKiller(initPID func(string) (int64, error)) *hostProcessKiller {
	return &hostProcessKiller{procRoot: execProcRoot, initPID: initPID, open: openPID}
}

// KillInContainer implements processKiller. Order matters: pin the process
// first, then compare cgroups and start time, then signal through the pin.
// A reuse that happens after the pin makes the signal fail (the pinned
// process is gone) instead of reaching the newcomer. The cgroup check
// catches a reuse by the host or another box; the start-time check catches
// a reuse inside the same box between the stamp and the pin (#2765): the
// pinned pid's /proc/<pid>/stat field 22 must not be later than the exec's
// boot-clock stamp, both CLOCK_BOOTTIME in USER_HZ ticks. This is the only
// stat read of the exec path: one per kill attempt, none per exec.
func (k *hostProcessKiller) KillInContainer(container string, pid uint32, stamp threatdetect.ProcStartTicks) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to signal pid %d", pid)
	}
	initPID, err := k.initPID(container)
	if err != nil {
		return fmt.Errorf("read init pid of %s: %w", container, err)
	}
	if initPID <= 0 {
		return fmt.Errorf("container %s is not running: %w", container, errKillProcessGone)
	}
	// Up front, before any pid is pinned or compared (#2768, decided on
	// #2781): only an Incus container has a payload cgroup that every one of
	// its processes sits under. On another runtime the compare below could
	// never hold and would misreport every kill as "pid reused".
	initRel, err := cgroupV2PathOf(k.procRoot, initPID)
	if err != nil {
		return fmt.Errorf("resolve cgroup of %s init %d: %w", container, initPID, err)
	}
	want, ok := incusPayloadRoot(initRel)
	if !ok {
		return fmt.Errorf("container %s init cgroup %q: %w", container, initRel, errKillRuntimeUnsupported)
	}
	if int64(pid) == initPID {
		return fmt.Errorf("pid %d: %w", pid, errKillContainerInit)
	}
	if stamp == 0 {
		return fmt.Errorf("pid %d: matched exec has no stamp: %w", pid, errKillStartTimeUnknown)
	}
	h, err := k.open(int(pid))
	if err != nil {
		return fmt.Errorf("pid %d: %w", pid, err)
	}
	defer func() { _ = h.Close() }()

	got, err := containerCgroupOf(k.procRoot, int64(pid))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("pid %d: %w", pid, errKillProcessGone)
		}
		return fmt.Errorf("resolve cgroup of pid %d: %w", pid, err)
	}
	if got != want {
		return fmt.Errorf("pid %d in %q, container %s in %q: %w", pid, got, container, want, errKillPIDReused)
	}
	// After the pin: while the pinned process lives it holds the pid, so
	// /proc/<pid> is that process; if it died since, /proc/<pid> is gone
	// (process gone) or a newcomer that started after the pin, hence after
	// the stamp (refused), and the pidfd signal would fail anyway.
	cur, err := readProcStart(k.procRoot, pid)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("pid %d: %w", pid, errKillProcessGone)
		}
		return fmt.Errorf("pid %d: read start time: %v: %w", pid, err, errKillStartTimeUnknown)
	}
	if startedAfterStamp(cur, stamp) {
		return fmt.Errorf("pid %d started at %d ticks since boot, after its matched exec was seen at %d: %w", pid, cur, stamp, errKillPIDReused)
	}
	if err := h.Kill(); err != nil {
