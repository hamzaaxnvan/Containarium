package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/connectcore"
	"github.com/footprintai/containarium/internal/sshkey"
)

// connectReadyTimeout bounds how long connect waits for a freshly-created box
// to finish coming up, and connectPollInterval is the gap between re-checks.
// An agent commonly creates a box then immediately connects, racing the
// box's bring-up — waiting absorbs that race instead of erroring. Kept under
// the typical MCP client request timeout (~60s) so a not-yet-ready box returns
// a clean "try again shortly" the agent can retry on, rather than blowing past
// the client deadline as an opaque -32001 (#837). vars (not consts) so tests
// can shrink them.
var (
	connectReadyTimeout = 40 * time.Second
	connectPollInterval = 3 * time.Second
)

// handleConnect is the agent-native half of `containarium connect` — the
// thin MCP wrapper over the same connectcore resolve+authorize core the
// CLI verb uses (the "one Go func, two surfaces" pattern, per CLAUDE.md).
//
// An MCP call is request/response with no PTY, so only the two
// non-interactive modes are surfaced:
//
//   - config mode (no `exec` arg): resolve the box, authorize the managed
//     key, and return the ready `ssh <user>@<host>` invocation. The
//     human's terminal connects.
//   - exec mode (`exec` arg): run one command over SSH and return its
//     stdout / stderr / exit_code — operate the box without a TTY.
//
// Interactive (PTY) stays CLI-only.
func handleConnect(client API, args map[string]interface{}) (string, error) {
	box := strings.TrimSpace(getStringArg(args, "box", ""))
	if box == "" {
		return "", fmt.Errorf("`box` is required")
	}
	execCmd := getStringArg(args, "exec", "")
	userOverride := getStringArg(args, "user", "")
