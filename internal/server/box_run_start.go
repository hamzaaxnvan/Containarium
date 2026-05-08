package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/agentbox"
	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/coderun/engine"
	"github.com/footprintai/containarium/pkg/core/toolchain"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// StartBoxRun (#2193): the daemon-driven counterpart to `containarium code
// run` for a caller with no SSH client. It root-execs a shell script on the
// box via the SAME mgr.ExecWithExitCode path ListBoxRuns/TailBoxRunLog
// already use — no SSH, no agent-box RPC — that replicates
// spawnBackgroundProcess's on-disk contract (atomic temp+rename RunRecord
// JSON, setsid-detached child, child-written .exit sidecar), so the existing
// readers see a daemon-started run exactly like a `code run`-started one.
//
// Unlike List/Tail (which only ever READ, as root, files the box's user
// owns), starting a run has to WRITE as that user: the spawned engine process
// needs that user's $HOME to find its own credentials, and the record/log it
// creates need to end up owned the same way an SSH-driven `code run` leaves
// them. So every write here goes through boxRunStartExecFn, which wraps the
// script in `su - <username> -c` — never boxRunExecFn's plain root exec.

// boxRunStartFunc is StartBoxRun's own exec seam: production wraps the
// container manager AND drops privilege to username via `su -`; tests fake
// it directly (ignoring username — a test sandbox has no real box user to
// drop into).
type boxRunStartFunc func(box, username, script string) (string, error)

// boxRunStartExecFn returns the exec seam: the injected fake, else the
// container manager wrapped with `su - <username> -c`.
func (s *ContainerServer) boxRunStartExecFn() boxRunStartFunc {
	if s.boxRunStartExec != nil {
		return s.boxRunStartExec
	}
	if s.manager == nil {
		return nil
	}
	mgr := s.manager
	return func(box, username, script string) (string, error) {
		wrapped := "su - " + shellSingleQuote(username) + " -c " + shellSingleQuote(script)
		out, stderr, code, err := mgr.ExecWithExitCode(box, []string{"sh", "-c", wrapped})
		if err != nil {
			return "", err
		}
		if code != 0 {
			return "", fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr))
		}
		return out, nil
	}
}

// boxRunHomeAndConfigScript prints the box user's $HOME on its own first
// line, then code.json's raw content (if it exists), if any — one round trip
// for both, since StartBoxRun always needs $HOME anyway (it's the run's cwd,
// and the base of every session-discovery path).
func boxRunHomeAndConfigScript() string {
	return `printf '%s\n' "$HOME"
cat "$HOME/.containarium/code.json" 2>/dev/null || true
`
}

// resolveBoxRunConfig reads $HOME and code.json off the box (as the box's own
// user) and resolves the engine+credential choice the box was installed
// with. A box with no code.json (every box installed before #1727) falls
// back to the same default `code run` does: Claude Code + a tenant secret —
// see readBoxCodeConfig's identical contract in internal/cmd/code_run_engine.go.
func resolveBoxRunConfig(exec boxRunStartFunc, box, username string) (home string, cfg *engine.CodeConfig, err error) {
	out, err := exec(box, username, boxRunHomeAndConfigScript())
	if err != nil {
		return "", nil, status.Errorf(codes.Unavailable, "read %s's $HOME/code.json on %q: %v", username, box, err)
	}
	home, rest, _ := strings.Cut(out, "\n")
	home = strings.TrimSpace(home)
	if home == "" {
		return "", nil, status.Errorf(codes.Unavailable, "could not resolve $HOME for %q on %q", username, box)
	}
	blob := strings.TrimSpace(rest)
	if blob == "" {
		return home, &engine.CodeConfig{
			Version:    engine.CodeConfigVersion,
			Engine:     engine.DefaultName,
			Credential: engine.DefaultKind,
		}, nil
	}
	cfg, perr := engine.ParseCodeConfig([]byte(blob))
	if perr != nil {
		return "", nil, status.Errorf(codes.FailedPrecondition, "%s on %q: %v", engine.CodeConfigPath, box, perr)
	}
	return home, cfg, nil
}

// boxRunSpawn is everything boxRunSpawnScript needs to render one run's
// launch script.
type boxRunSpawn struct {
	dir, name, command, home string
	// prelaunch runs before the engine is launched (the session-directory
	// snapshot a fresh run's discovery compares against). Empty for none.
	prelaunch string
	// sessionTrailer is appended verbatim after the pid/boot line: either a
	// literal sidecar write (a resumed run, whose session id is known from
	// the request) or the backgrounded discovery poll (a fresh run). Safe to
	// run unconditionally: a literal write is instant, the poll detaches.
	sessionTrailer string
	// structured keeps stderr in <name>.stderr.log so <name>.log is exactly
