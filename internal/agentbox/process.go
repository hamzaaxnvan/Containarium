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
				"the log wants 'combined'."),
			mcp.Enum("combined", "framed"),
		),
	)
}

func handleProcessStart(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	command, _ := args["command"].(string)
	if command == "" {
		return mcp.NewToolResultError("process_start: 'command' is required"), nil
	}
	name, _ := args["name"].(string)
	cwd, _ := args["cwd"].(string)
	captureModeArg, _ := args["capture_mode"].(string)
	captureMode, err := parseCaptureMode(captureModeArg)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("process_start: %v", err)), nil
	}

	actor, _ := args["actor"].(string)
	delegationChain, _ := args["delegation_chain"].(string)

	mp, err := spawnBackgroundProcess(name, command, cwd, captureMode, actor, delegationChain)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("process_start: %v", err)), nil
	}

	body := fmt.Sprintf(
		"name: %s\npid: %d\ncommand: %s\nlog_path: %s\nstarted_at: %s\n",
		mp.Name, mp.PID, mp.Command, mp.LogPath, mp.StartedAt.UTC().Format(time.RFC3339),
	)
	return mcp.NewToolResultText(body), nil
}

// spawnBackgroundProcess starts command under /bin/sh -c, detached via
// Setsid, capturing stdout+stderr to a log file under processLogDir per
// captureMode (#1674), and registers it in processRegistry under name
// (auto-generated if empty). Reaped asynchronously — a goroutine calls
// cmd.Wait() — so it never zombies.
//
// Shared core between the process_start MCP tool (handleProcessStart) and
// SpawnService.Spawn (gRPC, #1488 Phase 2): one implementation, two
// transports, so a caller reaching agent-box over the fast local socket
// gets identical spawn/reap semantics to one reaching it over MCP/SSH.
func spawnBackgroundProcess(name, command, cwd string, captureMode CaptureMode, actor, delegationChain string) (*managedProcess, error) {
	if command == "" {
		return nil, fmt.Errorf("'command' is required")
	}

	processRegistryMu.Lock()
	defer processRegistryMu.Unlock()

	if name == "" {
		name = fmt.Sprintf("proc-%d", time.Now().UnixNano())
	}
	if _, exists := processRegistry[name]; exists {
		return nil, fmt.Errorf("%w: a process named %q is already running; kill it first or pick a different name", ErrProcessNameInUse, name)
	}

	// Collision check against durable records, not just the in-memory
	// registry: after a reconnect the registry is empty, so without this a
	// reused name would pass the check above and O_TRUNC the previous run's
	// log below — destroying exactly the output #1672 exists to preserve.
	if existing, found, err := readRunRecord(name); err != nil {
		return nil, fmt.Errorf("check existing run record for %q: %w", name, err)
	} else if found {
		switch ResolveOutcome(existing, bootID, isAlive) {
		case RunOutcomeRunning, RunOutcomeUnknown:
			// unknown is refused too: it is the only evidence of an
			// unresolved run (A2), and destroying it here would make that
			// run's outcome unrecoverable forever.
			return nil, fmt.Errorf("%w: a process named %q is already running; kill it first or pick a different name", ErrProcessNameInUse, name)
		case RunOutcomeExited:
			if err := rotateFinishedRun(existing); err != nil {
				return nil, fmt.Errorf("rotate finished run %q: %w", name, err)
			}
		}
	}

	// Captured once, here, and used for the rest of this run's lifecycle
	// (including the async reap goroutine below) instead of re-reading the
	// processLogDir package var later: that var may legitimately be a
	// different value by the time the reaper runs (tests repoint it per
	// test; nothing stops a future caller from doing the same), and an
	// unsynchronized cross-goroutine read of it would be a data race
	// regardless.
	logDir := processLogDir
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", logDir, err)
	}
	logPath := filepath.Join(logDir, sanitizeName(name)+".log")
	// #nosec G304 -- sanitizeName strips every char outside [A-Za-z0-9_.-],
	// so logPath is always logDir/<safe>.log (logDir is /tmp/agent-box in
	// production, a test's own TempDir in tests). Path traversal is not
	// reachable from this construction.
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log %s: %w", logPath, err)
	}

	// #nosec G204 -- spawning agent-supplied commands is the entire
	// feature, on both transports this function serves.
	// Wrap so the CHILD records its own exit status (#1693). The reap
	// goroutine below only runs while this agent-box is alive, and
	// agent-box dies with its SSH connection — so for a detached run (the
	// whole point of #1672) nothing in-process survives to write the
	// outcome. The setsid'd child does, so it writes the sidecar itself.
	// Framed capture frames on the CHILD's side of the fork (#1701). Handing
	// exec.Cmd an io.Writer instead of an *os.File makes it insert a pipe and
	// a copier goroutine inside agent-box; when the SSH connection drops and
	// agent-box exits, the child dies of SIGPIPE on its next write. So both
	// modes now give the child a real descriptor: combined writes the log
	// directly, framed writes FIFOs that setsid'd framer children drain.
	var framing *framingSpec
	if captureMode == CaptureFramed {
		self, err := agentBoxSelfPath()
		if err != nil {
			_ = logFile.Close()
			return nil, fmt.Errorf("locate agent-box for framed capture: %w", err)
		}
		framing = &framingSpec{
			agentBox: self,
			logPath:  logPath,
			outFIFO:  filepath.Join(logDir, sanitizeName(name)+".out.fifo"),
			errFIFO:  filepath.Join(logDir, sanitizeName(name)+".err.fifo"),
		}
	}

	// #nosec G204 -- spawning agent-supplied commands is the entire feature.
	cmd := exec.Command("/bin/sh", "-c", buildRunScript(command, exitSidecarPath(logDir, name), framing))
	if cwd != "" {
		cmd.Dir = cwd
	}
	// Both modes hand the child a real *os.File. In framed mode the child's
	// own stdout/stderr are redirected to the FIFOs by the script, so these
	// only carry anything the script itself emits (e.g. an mkfifo failure).
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Detach from agent-box's process group so a child of the spawned
	// process survives an agent-box restart and can be reaped later.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
