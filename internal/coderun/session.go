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
