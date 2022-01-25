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
