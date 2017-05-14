package coderun

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// tailLogContentMarker is tail_log's own literal separator between its
// key:value header and raw file content (internal/agentbox/tail_log.go).
const tailLogContentMarker = "--- content ---\n"

// agentBoxPathPrefix puts the two directories agent-box can be installed into
// on PATH for the remote command. `containarium code install` writes to
// ~/.local/bin (user-level, no root); the agent-runtime recipe's own
// installer writes to /usr/local/bin. A non-interactive `ssh host agent-box`
// runs a non-login shell and therefore does NOT get the box's login-shell
// PATH, so without this a perfectly well-installed helper is simply not
// found.
const agentBoxPathPrefix = `PATH="$HOME/.local/bin:/usr/local/bin:$PATH"`

// AgentBoxPathPrefix is agentBoxPathPrefix for commands built outside this
// package that start agent-box on the box (the approval server, #2183).
const AgentBoxPathPrefix = agentBoxPathPrefix

// AgentBoxRemoteCommand is the remote command used by code run and quickstart's
// MCP configs. exec replaces the wrapping shell so agent-box owns the stdio MCP
// speaks over, and so a signal reaches the helper rather than an intermediate shell.
const AgentBoxRemoteCommand = `sh -c '` + agentBoxPathPrefix + ` exec agent-box'`

// agentBoxProbeCommand answers one question after a failed handshake: is the
// helper there at all? Distinguishing "not installed" from "installed but the
// session broke" is the whole difference between an actionable error and the
// raw `transport closed` users used to get.
const agentBoxProbeCommand = `sh -c '` + agentBoxPathPrefix + ` command -v agent-box >/dev/null 2>&1'`

// ErrAgentBoxMissing reports that a box has no agent-box helper on PATH.
// Callers match it with errors.Is and render AgentBoxMissingError, which is
// the message a user can act on.
var ErrAgentBoxMissing = errors.New("agent-box is not installed")

// AgentBoxMissingError names the box and the command that fixes it. Every
// surface that opens a session (the CLI and the platform MCP tools) renders
// the same sentence, so the fix does not depend on which one you used.
func AgentBoxMissingError(box string) error {
	return fmt.Errorf("%w on %q; run `containarium code install %s`", ErrAgentBoxMissing, box, box)
}

// mcpConn is the slice of *client.Client this package actually uses. Named as
// an interface so dial()'s failure classification is testable without a box,
// an ssh binary, or a live handshake.
type mcpConn interface {
	Initialize(context.Context, mcp.InitializeRequest) (*mcp.InitializeResult, error)
	CallTool(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	Close() error
}

// dialMCP spawns ssh and speaks MCP over its stdio. A package var purely as a
// test seam; production never reassigns it.
var dialMCP = func(args []string) (mcpConn, error) {
	// #nosec G204 -- args is built by the caller from a daemon-resolved
	// target plus a fixed remote-command literal, never caller input.
	return client.NewStdioMCPClient("ssh", nil, args...)
}

// probeAgentBox runs one `command -v agent-box` over the same ssh path. A
// non-nil return means the helper could not be found. A probe that cannot run
// at all (no local ssh) returns nil: claiming "not installed" on the strength
// of a probe that never executed would be worse than the transport error.
var probeAgentBox = func(ctx context.Context, sshArgs []string) error {
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return nil
	}
	args := append(append([]string{}, sshArgs...), agentBoxProbeCommand)
	// #nosec G204 -- see dialMCP; agentBoxProbeCommand is a fixed literal.
	return exec.CommandContext(ctx, sshBin, args...).Run()
}

// Session is a live MCP connection to one box's agent-box, reached the same
// way any MCP client (Claude Code, Claude Desktop) reaches it: spawn
// `ssh <sshArgs...> -- agent-box` as a subprocess and speak MCP over its
// stdio (docs/K8S-AGENT-BOX-RUNTIME-DESIGN.md). Implements LogReader
// directly, transparently reconnecting once on a transport-level failure
// (a dropped SSH connection) before surfacing an error — StreamOutput's own
// retry loop is the second line of defense for anything that doesn't
// self-heal on one reconnect attempt.
type Session struct {
	sshArgs []string // ssh's own flags/target; "agent-box" is appended as the remote command

	mu  sync.Mutex
	mcp mcpConn
}

// Connect spawns ssh with sshArgs (host/user/identity/port flags — NOT
// including a remote command) plus "agent-box" appended as the remote
// command, and initializes an MCP session over its stdio.
func Connect(ctx context.Context, sshArgs []string) (*Session, error) {
	s := &Session{sshArgs: sshArgs}
	c, err := s.dial(ctx)
	if err != nil {
		return nil, err
	}
	s.mcp = c
	return s, nil
}

func (s *Session) dial(ctx context.Context) (mcpConn, error) {
	args := append(append([]string{}, s.sshArgs...), AgentBoxRemoteCommand)
	c, err := dialMCP(args)
	if err != nil {
		return nil, fmt.Errorf("start agent-box over ssh: %w", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "containarium-code", Version: "1"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		_ = c.Close()
		// A box with no agent-box and a box whose ssh connection died look
		// identical here: the far end simply never speaks MCP. One probe
		// tells them apart, and only the first has an obvious fix.
		if probeErr := probeAgentBox(ctx, s.sshArgs); probeErr != nil {
			return nil, fmt.Errorf("%w (initialize MCP session: %v)", ErrAgentBoxMissing, err)
		}
		return nil, fmt.Errorf("initialize MCP session: %w", err)
	}
	return c, nil
}

// Close ends the underlying ssh subprocess and MCP session.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mcp.Close()
}

// callTool invokes name with args and returns its concatenated text
// content. reconnect=true retries once over a freshly-dialed session on
// any error (transport-level failures — a dead ssh subprocess, a closed
// pipe — look identical to a tool-level error at this layer, so the retry
// is harmless when it wasn't actually a transport failure: a genuine
// application error simply repeats).
func (s *Session) callTool(ctx context.Context, name string, args map[string]any, reconnect bool) (string, error) {
	s.mu.Lock()
	c := s.mcp
	s.mu.Unlock()

	text, err := doCallTool(ctx, c, name, args)
	if err == nil || !reconnect {
		return text, err
	}

