//go:build !windows

package agentbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// tail_log lets an agent watch a file as new content is appended,
// bounded by a wall-clock follow window. The MCP transport is
// request/response (no streaming), so this tool is the request/response
// equivalent of "tail -f for N seconds": collect what arrived during
// the window and return it.
//
// Typical use: after `shell_exec systemctl start caddy`, the agent
// calls tail_log on /var/log/caddy/access.log with follow_seconds=10
// to confirm the service actually started serving (or to capture the
// crash message if it didn't).
const (
	tailLogDefaultFollowSec = 10
	tailLogMaxFollowSec     = 60
	tailLogOutputLimit      = 256 * 1024 // 256 KiB — same as shell_exec
	tailLogPollInterval     = 200 * time.Millisecond
)

func registerTailLogTool(s *server.MCPServer) {
	s.AddTool(tailLogTool(), handleTailLog)
}

func tailLogTool() mcp.Tool {
	return mcp.NewTool(
		"tail_log",
		mcp.WithDescription(
			"Watch a file as new content is appended, bounded by follow_seconds. "+
				"Returns the bytes written to the file during the window plus a new "+
				"end_offset suitable for resuming on the next call. If start_offset "+
				"is omitted, watching begins at the current end-of-file (\"tail -f "+
				"from now\"). Capped at 256 KiB output; longer streams set the "+
				"truncated flag — the agent can resume by passing the returned "+
				"end_offset as start_offset on the next call.",
		),
		mcp.WithString("path",
			mcp.Description("Absolute or relative path to the log file."),
			mcp.Required(),
		),
		mcp.WithNumber("start_offset",
			mcp.Description("Byte offset to start reading from. Default: file size at call time (i.e. only new content)."),
		),
		mcp.WithNumber("follow_seconds",
			mcp.Description(fmt.Sprintf("How long to watch for new content. Default %d, max %d.",
				tailLogDefaultFollowSec, tailLogMaxFollowSec)),
			mcp.DefaultNumber(float64(tailLogDefaultFollowSec)),
		),
	)
}

func handleTailLog(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	rawPath, ok := args["path"].(string)
	if !ok || rawPath == "" {
		return mcp.NewToolResultError("tail_log: 'path' is required"), nil
	}
	path, err := validatePathCtx(ctx, rawPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("tail_log: %v", err)), nil
	}

	followSec := float64(tailLogDefaultFollowSec)
	if v, ok := args["follow_seconds"].(float64); ok && v > 0 {
		followSec = v
		if followSec > tailLogMaxFollowSec {
			followSec = tailLogMaxFollowSec
		}
	}

	info, err := os.Stat(path)
