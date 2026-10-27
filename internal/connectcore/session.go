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
  printf 'CNTR_DECODE_FAIL_%s\n' "$MARKER"; exit 2
fi
tmux has-session -t "$SESSION" 2>/dev/null || tmux new-session -d -s "$SESSION"
tmux send-keys -t "$SESSION" "source '$CMDF' > '$OUTF' 2>&1; echo \$? > '$RCF'" Enter
i=0; lim=$((TIMEOUT*5))
while [ ! -s "$RCF" ]; do
  i=$((i+1))
  if [ "$i" -gt "$lim" ]; then break; fi
  sleep 0.2
done
RC="timeout"
if [ -s "$RCF" ]; then RC="$(tr -d '[:space:]' < "$RCF")"; fi
printf 'CNTR_RC_%s=%s\n' "$MARKER" "$RC"
printf 'CNTR_OUT_BEGIN_%s\n' "$MARKER"
[ -f "$OUTF" ] && cat "$OUTF"
printf '\nCNTR_OUT_END_%s\n' "$MARKER"
rm -f "$CMDF" "$OUTF" "$RCF"
`

// SessionExecScript returns the orchestration script to pipe to `bash -s`
// on the box.
func SessionExecScript() string { return sessionExecScript }

// BuildSessionExecArgs returns the ssh argv that runs the orchestration on
// the box: ssh opts + user@host + `bash -s -- <session> <marker> <b64> <timeout>`.
// The script itself is supplied on stdin by the caller.
func BuildSessionExecArgs(t Target, identity, session, marker, b64cmd string, timeoutSec int) []string {
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	args := BuildSSHArgs(t, identity, "") // opts + user@host, no remote command tail
	return append(args, "bash", "-s", "--", session, marker, b64cmd, strconv.Itoa(timeoutSec))
}

// BuildAttachArgs returns the ssh argv that attaches a human terminal to
// the named tmux session, creating it if absent (`new-session -A`). `-t`
// forces a PTY so the attach is interactive.
func BuildAttachArgs(t Target, identity, session string) []string {
	args := []string{"-t", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=accept-new"}
	if identity != "" {
		args = append(args, "-i", identity)
	}
	if t.Port != 0 && t.Port != 22 {
		args = append(args, "-p", strconv.Itoa(t.Port))
	}
	return append(args, t.User+"@"+t.Host, "tmux", "new-session", "-A", "-s", session)
}

// ParseSessionResult extracts stdout + exit code from the script's framed
// output. Returns ErrNoTmux / ErrSessionTimeout for those terminal states.
func ParseSessionResult(raw, marker string) (stdout string, exitCode int, err error) {
	if strings.Contains(raw, "CNTR_NO_TMUX_"+marker) {
		return "", 0, ErrNoTmux
	}
	if strings.Contains(raw, "CNTR_DECODE_FAIL_"+marker) {
		return "", 0, fmt.Errorf("the box failed to decode the command")
	}

	// stdout is framed between BEGIN and END delimiters.
	beginTag := "CNTR_OUT_BEGIN_" + marker + "\n"
	endTag := "\nCNTR_OUT_END_" + marker
	if b := strings.Index(raw, beginTag); b >= 0 {
		after := raw[b+len(beginTag):]
		if e := strings.Index(after, endTag); e >= 0 {
			stdout = after[:e]
		} else {
			stdout = after
		}
	}

	rcTag := "CNTR_RC_" + marker + "="
	idx := strings.Index(raw, rcTag)
	if idx < 0 {
		return stdout, 0, fmt.Errorf("malformed session result (no exit-code marker)")
	}
	rcLine := raw[idx+len(rcTag):]
	if nl := strings.IndexByte(rcLine, '\n'); nl >= 0 {
		rcLine = rcLine[:nl]
	}
	rcStr := strings.TrimSpace(rcLine)
	if rcStr == "timeout" {
		return stdout, 0, ErrSessionTimeout
	}
	code, convErr := strconv.Atoi(rcStr)
	if convErr != nil {
		return stdout, 0, fmt.Errorf("unparseable exit code %q", rcStr)
	}
	return stdout, code, nil
}
