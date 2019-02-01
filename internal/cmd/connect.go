package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/connectcore"
	"github.com/footprintai/containarium/internal/sshkey"
	"github.com/spf13/cobra"
)

// `containarium connect <box>` turns the token the caller already holds
// into a shell, without the user ever hand-managing an SSH key. It
// resolves the box, authorizes a managed key on the caller's behalf, and
// then either drops into an interactive session (default), runs one
// command and returns its output (--exec, the agent-native path), or just
// prints the ready ssh invocation (--print).
//
// CLI-first per CLAUDE.md: this is the canonical surface; the MCP `connect`
// tool is a thin wrapper over the same connectcore resolve+authorize core.
//
// It is glue over primitives the daemon already serves:
//   - GET  /v1/containers/{box}            → ssh_host, username, state, IP
//   - POST /v1/containers/{box}/ssh-keys   → authorize a key (idempotent)
var (
	connectServer   string
	connectExec     string
	connectPrint    bool
	connectKeyPath  string
	connectIdentity string
	connectUser     string
	connectHost     string
	connectPort     int
	connectSession  string
	connectKeyWait  string
)

var connectCmd = &cobra.Command{
	Use:   "connect <box>",
	Short: "Open a shell on a box using your token (no SSH-key setup)",
	Long: `Connect to one of your boxes over SSH using the token you're already
logged in with — connect authorizes a managed key for you, so you never
have to set up or copy an SSH key yourself.

Modes:

  containarium connect my-box                       # interactive shell
  containarium connect my-box --exec "make"         # run one command, return its output
  containarium connect my-box --print               # authorize + print the ssh command, don't connect

Stateful sessions (--session) run inside a named tmux session ON THE BOX,
so state (cd, exports, background jobs) persists across calls and you can
attach a terminal to watch:

  containarium connect my-box --session work --exec "cd /app"   # state persists...
  containarium connect my-box --session work --exec "pwd"       # ...to the next call → /app
  containarium connect my-box --session work                    # attach your terminal to it

The SSH target is the box's ssh_host (or its IP if the daemon reports no
ssh_host) and its SSH username. Override either with --host / --user.`,
	Args: cobra.ExactArgs(1),
	RunE: runConnect,
}

func init() {
	connectCmd.Flags().StringVar(&connectServer, "server", "", "server to talk to (default: your logged-in server)")
	connectCmd.Flags().StringVar(&connectExec, "exec", "", "run one command over SSH and return its output (non-interactive)")
	connectCmd.Flags().BoolVar(&connectPrint, "print", false, "authorize the key and print the ready ssh command; do not connect")
	connectCmd.Flags().StringVar(&connectKeyPath, "key", "", "public key to authorize (default: reuse or generate the managed key)")
	connectCmd.Flags().StringVar(&connectIdentity, "identity", "", "private key to authenticate with (default: derived from the public key)")
	connectCmd.Flags().StringVar(&connectUser, "user", "", "override the SSH login user (default: the box's SSH username)")
	connectCmd.Flags().StringVar(&connectHost, "host", "", "override the SSH host (default: the box's ssh_host, else its IP)")
	connectCmd.Flags().IntVar(&connectPort, "port", 22, "SSH port")
	connectCmd.Flags().StringVar(&connectSession, "session", "", "run inside a named tmux session on the box (stateful; persists across calls). With --exec runs the command there; without --exec attaches your terminal.")
