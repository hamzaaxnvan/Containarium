//go:build !windows

package agentbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Exit sidecar (#1693). The reap goroutine in spawnBackgroundProcess runs
// INSIDE agent-box, and agent-box is a stdio MCP server spawned per SSH
// connection — so when the connection drops, the reaper dies with it while
// the setsid'd child keeps running. Its exit status was then never written
// anywhere, and every detached run reported "unknown" forever: precisely
// the case #1672 exists for.
//
// The child outlives the connection, so the child records its own outcome.
// spawnBackgroundProcess wraps the command in a shell that writes its exit
// status to "<log-dir>/<name>.exit" on completion, and readRunRecord folds
// that sidecar into a record whose ExitCode is still nil.
//
// What deliberately still yields "unknown": a run whose wrapper shell was
// itself SIGKILLed, or a box that died mid-run. Nothing wrote a status
// because nothing survived to write one — and an OOM must stay
// distinguishable from a clean exit (design doc Part A, row 3).

// exitSidecarPath returns the sidecar path for a run in dir.
func exitSidecarPath(dir, name string) string {
	return filepath.Join(dir, sanitizeName(name)+".exit")
}

// framingSpec describes the child-side framing a run needs. nil means
// combined mode (the child writes the log file directly).
type framingSpec struct {
	agentBox string // absolute path to this agent-box binary
	logPath  string
	outFIFO  string
	errFIFO  string
}

// buildRunScript composes the shell program a spawned run actually executes:
// the caller's command, its exit status durably recorded (#1693), and — in
// framed mode — the framing done by child processes rather than by agent-box
// (#1701).
//
// The command runs in a SUBSHELL — "( … )", not "{ …; }". A command ending in
// `exit N` (or any `exit` on an error path) terminates the whole shell from
// inside a brace group, so the sidecar write is never reached and #1693
// reappears for exactly the commands most likely to report a meaningful
// status. A subshell confines that exit and hands $? back.
//
// $? is captured into $__cx on the line immediately after the command, before
// anything else — `wait` and the sidecar write both clobber it otherwise. The
// script exits with the original code, so a reaper that IS still alive sees
// the true status through cmd.Wait() and the in-memory path is unchanged.
//
// In framed mode the two framers are started BEFORE the command and read from
// FIFOs it writes to. Opening a FIFO blocks until both ends are open, so the
// readers must be running first. They are children of this shell, which
