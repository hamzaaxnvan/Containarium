//go:build !windows

package agentbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ErrProcessNameInUse is returned by spawnBackgroundProcess when name
// already names a registered process. Sentinel (rather than a plain
// fmt.Errorf) because SpawnService.Spawn (gRPC, #1488 Phase 2) needs to
// map it to codes.AlreadyExists specifically — a client-correctable
// conflict, not a server malfunction — without string-matching the
// message. The MCP handler doesn't need to distinguish it (it only
// surfaces the message text), so it keeps using %v/%w against this error
// like any other.
var ErrProcessNameInUse = errors.New("process name already in use")

// Process management.
//
// shell_exec is bounded — runs a single command for up to 10 minutes and
// returns its output. process_start fills the gap when an agent needs
// something long-lived: a dev server, a build, a watcher. The agent
// kicks it off with process_start, watches it via tail_log on the
// returned log_path, and reaps it with process_kill.
//
// State lives in-memory (a package-level registry). Processes survive
// tool calls but NOT agent-box restarts. Output is captured to
// /tmp/agent-box/<name>.log so even after the process exits the agent
// can still read what it produced (until /tmp gets cleared).

// processLogDir is a var, not a const, so tests can point it at t.TempDir()
// (#1672 design doc, finding A1) — run-record tests assert on-disk state
// and must stay isolated from the real /tmp/agent-box to be parallel-safe.
var processLogDir = defaultProcessLogDir()

// defaultProcessLogDir resolves where run logs, records, and exit sidecars
// live. AGENTBOX_LOG_DIR overrides it — needed by tests that run a REAL
// agent-box subprocess (the only way to reproduce the parent-exit case behind
// #1701, since an in-process spawn keeps the parent alive), and useful to an
// operator who does not want run state on /tmp.
// agentBoxSelfPath resolves the agent-box binary the framer children exec
// (#1701). os.Executable() is right in production — agent-box spawns the run —
// but it is whatever binary embeds this package, which is not agent-box under
// `go test`, nor in any host process that imports agentbox as a library.
// AGENTBOX_SELF names it explicitly for those cases.
func agentBoxSelfPath() (string, error) {
	if p := os.Getenv("AGENTBOX_SELF"); p != "" {
		return p, nil
	}
	return os.Executable()
}

func defaultProcessLogDir() string {
	if dir := os.Getenv("AGENTBOX_LOG_DIR"); dir != "" {
		return dir
	}
	return DefaultLogDir
}

// DefaultLogDir is where run logs, records, and exit sidecars live unless
// AGENTBOX_LOG_DIR says otherwise. The daemon's box-run reader (#2123) reads
// the same directory from outside the box.
const DefaultLogDir = "/tmp/agent-box"

const processKillWaitTime = 2 * time.Second

type managedProcess struct {
	Name      string
	PID       int
	Command   string
	StartedAt time.Time
	LogPath   string

	cmd *exec.Cmd
}

var (
	processRegistry   = make(map[string]*managedProcess)
	processRegistryMu sync.Mutex
)

// reapWG tracks in-flight reap goroutines (cmd.Wait() + the final
// writeRunRecordAt). Production has no reason to wait on it — the whole
// point of async reaping is not blocking the caller — but tests that kill a
// process and then tear down its (TempDir-backed) processLogDir need to
// know the reaper's write has actually landed first; see
// waitForReapersForTest.
var reapWG sync.WaitGroup

func registerProcessTools(s *server.MCPServer) {
	s.AddTool(processStartTool(), handleProcessStart)
	s.AddTool(processListTool(), handleProcessList)
	s.AddTool(processKillTool(), handleProcessKill)
}

// ----- process_start ---------------------------------------------------

func processStartTool() mcp.Tool {
	return mcp.NewTool(
		"process_start",
		mcp.WithDescription(
			"Spawn a long-running shell command in the background. Returns the "+
				"process name (caller-supplied or auto-generated), PID, and a log "+
				"path under /tmp/agent-box where stdout+stderr are captured. The "+
				"agent watches output via tail_log on the log_path and reaps the "+
				"process via process_kill. Use this when the work outlives a single "+
				"shell_exec call — dev servers, watchers, builds.",
		),
		mcp.WithString("command",
			mcp.Description("Shell command to spawn, e.g. 'npm run dev' or 'caddy run'."),
			mcp.Required(),
		),
		mcp.WithString("name",
			mcp.Description("Stable identifier for later list/kill calls. Auto-generated if omitted. Must be unique across active processes."),
		),
		mcp.WithString("cwd",
			mcp.Description("Working directory for the spawned process. Default: agent-box's cwd."),
		),
		mcp.WithString("actor",
			mcp.Description("Optional provenance: the principal that authorized this run, recorded on the run record so a later reconnect can say who started it. CALLER-ASSERTED — agent-box cannot verify it (no authenticated context on this transport), so it is weaker evidence than a platform audit row. Omit rather than guess: empty records the run as unattributed, which is better than attributing it wrongly."),
		),
		mcp.WithString("delegation_chain",
			mcp.Description("Optional JSON-serialized delegation chain behind `actor` (the act claim shape). Same caller-asserted caveat."),
		),
		mcp.WithString("capture_mode",
			mcp.Description("How to capture stdout+stderr: 'combined' (default) writes plain text, "+
				"readable with a plain tail/cat. 'framed' interleaves stdout and stderr as "+
				"line-framed, base64-encoded records a client can demultiplex — opt in only if "+
				"you're driving this process through a demuxing client (#1674); a human tailing "+
