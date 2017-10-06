//go:build !windows

package agentbox

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// shellExecDefaultTimeout bounds runaway commands. The agent can override
// per-call up to shellExecMaxTimeout. We don't allow truly unbounded execs
// because the MCP transport stays open while the command runs and a 6-hour
// hang would brick the agent's session.
const (
	shellExecDefaultTimeout = 30 * time.Second
	shellExecMaxTimeout     = 10 * time.Minute
	shellExecOutputLimit    = 256 * 1024 // 256 KiB per stream — protects MCP from megabyte-scale dumps
)

func registerShellTool(s *server.MCPServer) {
	tool := mcp.NewTool(
		"shell_exec",
		mcp.WithDescription(
			"Execute a shell command inside the Containarium box. Runs under /bin/sh -c, "+
				"captures stdout and stderr separately, and returns the exit code. "+
				"Use this for any system operation that doesn't have a dedicated tool: "+
				"installing packages (apt, npm, pip), inspecting state (ps, ss, df), "+
				"running build steps, etc. For long-lived tail/follow commands, use "+
				"tail_log instead — shell_exec is one-shot and bounded by timeout_seconds.",
		),
		mcp.WithString("command",
			mcp.Description("Shell command to execute, e.g. 'ls -la /etc'"),
			mcp.Required(),
		),
		mcp.WithNumber("timeout_seconds",
			mcp.Description(fmt.Sprintf("Hard timeout in seconds. Default %d, max %d.",
				int(shellExecDefaultTimeout.Seconds()), int(shellExecMaxTimeout.Seconds()))),
			mcp.DefaultNumber(float64(shellExecDefaultTimeout.Seconds())),
		),
		mcp.WithString("cwd",
			mcp.Description("Working directory for the command. Default: process cwd (the box's home directory)."),
		),
	)
	s.AddTool(tool, handleShellExec)
}

func handleShellExec(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return mcp.NewToolResultError("shell_exec: 'command' is required and must be a non-empty string"), nil
	}

	timeout := shellExecDefaultTimeout
	if v, ok := args["timeout_seconds"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Second
		if timeout > shellExecMaxTimeout {
			timeout = shellExecMaxTimeout
		}
	}

	cwd, _ := args["cwd"].(string)

	res := runShellCommand(ctx, command, cwd, timeout)

	// Encode result as a single text block — agents parse this readily and
	// MCP clients render it inline. Using structured key=value sections so
	// the agent can grep for "exit_code:" without regex over JSON escapes.
	body := fmt.Sprintf(
		"exit_code: %d\n"+
			"timeout_seconds: %d\n"+
			"--- stdout ---\n%s\n"+
			"--- stderr ---\n%s",
		res.ExitCode, int(timeout.Seconds()), res.Stdout, res.Stderr,
	)
	return mcp.NewToolResultText(body), nil
}

// shellExecResult is the outcome of running one command to completion.
type shellExecResult struct {
	Stdout   string
	Stderr   string
