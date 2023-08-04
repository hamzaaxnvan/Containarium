package connectcore

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Tier 2 — stateful sessions hosted as a named tmux session ON THE BOX.
//
// Tier-1 --exec is stateless: each call is an independent `ssh "<cmd>"`, so
// `cd /foo` then `ls` across two calls loses state. Tier 2 runs the command
// inside a persistent tmux session so state (cd, exports, background jobs)
// survives between calls — and a human can `tmux attach` to the same
// session and watch.
//
// Mechanism (one ssh round-trip per exec; the orchestration runs on the
// box via `bash -s`, the script piped on stdin):
//   - the command is base64-encoded so it survives every shell layer
//     unquoted;
//   - it is `source`d into the session's live shell (NOT run in a
//     subshell) so cd/exports persist — that is the whole point of Tier 2;
//   - stdout+stderr go to a temp file, the exit code to a second file;
//     the script polls for the rc file, then frames the result with
//     marker-tagged delimiters that ParseSessionResult reads back.

var (
	// ErrNoTmux means the box image has no tmux — Tier 2 can't run there.
	ErrNoTmux = errors.New("tmux is not installed on the box (Tier-2 sessions need it; use plain --exec for a stateless command)")
	// ErrSessionTimeout means the command didn't finish before the box-side
	// poll gave up (e.g. an interactive command waiting on input).
	ErrSessionTimeout = errors.New("command did not finish before the timeout")
)

// NewMarker returns a random hex token tagging one session-exec invocation
// (temp filenames + output delimiters). crypto/rand-backed so command
// output can't accidentally collide with a delimiter.
func NewMarker() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate marker: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// EncodeCommand base64-encodes a command so it passes through ssh + the
// remote shell + tmux without any quoting.
func EncodeCommand(cmd string) string {
	return base64.StdEncoding.EncodeToString([]byte(cmd))
}

// ValidateSessionName guards the tmux session name. tmux rejects '.' and
// ':' in names; we allow the safe subset and keep it short.
func ValidateSessionName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" {
		return fmt.Errorf("session name is empty")
	}
	if len(n) > 64 {
		return fmt.Errorf("session name %q exceeds 64 characters", n)
	}
	for _, r := range n {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			continue
		default:
			return fmt.Errorf("session name %q contains disallowed character %q (allowed: alphanumerics, '-', '_')", n, r)
		}
	}
	return nil
}

// sessionExecScript is the fixed orchestration run on the box, parameterized
// at runtime by positional args: $1 session, $2 marker, $3 base64 command,
// $4 timeout seconds. `\$?` is escaped so it reaches the tmux pane shell
// literally (evaluated there, after `source` completes) rather than being
// expanded by this orchestration shell.
const sessionExecScript = `set -u
SESSION="$1"; MARKER="$2"; B64="$3"; TIMEOUT="${4:-60}"
if ! command -v tmux >/dev/null 2>&1; then
  printf 'CNTR_NO_TMUX_%s\n' "$MARKER"; exit 127
fi
DIR="${TMPDIR:-/tmp}"
CMDF="$DIR/cntr-$MARKER.cmd"; OUTF="$DIR/cntr-$MARKER.out"; RCF="$DIR/cntr-$MARKER.rc"
if ! printf '%s' "$B64" | base64 -d > "$CMDF" 2>/dev/null; then
