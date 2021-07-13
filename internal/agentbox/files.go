//go:build !windows

package agentbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// readFileLimit is the maximum bytes returned in a single read_file call.
// Agents that need more pull additional ranges via offset/limit. The cap
// keeps a single tool call below MCP's reasonable message size.
const readFileLimit = 512 * 1024 // 512 KiB

func registerFileTools(s *server.MCPServer) {
	s.AddTool(readFileTool(), handleReadFile)
	s.AddTool(writeFileTool(), handleWriteFile)
	s.AddTool(listDirectoryTool(), handleListDirectory)
	s.AddTool(moveFileTool(), handleMoveFile)
	s.AddTool(deleteFileTool(), handleDeleteFile)
}

// ----- read_file -------------------------------------------------------

func readFileTool() mcp.Tool {
	return mcp.NewTool(
		"read_file",
		mcp.WithDescription(
			"Read a file from the Containarium box's filesystem. Returns up to "+
				"512 KiB. Three modes: byte-range (offset+limit), line-head (head=N "+
				"first lines), or line-tail (tail=N last lines). head and tail are "+
				"mutually exclusive and override offset/limit when set. Binary files "+
				"are returned as-is — the caller should detect content type if it matters.",
		),
		mcp.WithString("path",
			mcp.Description("Absolute or relative path to the file."),
			mcp.Required(),
		),
		mcp.WithNumber("offset",
			mcp.Description("Byte offset to start reading from. Default 0. Ignored when head/tail set."),
			mcp.DefaultNumber(0),
		),
		mcp.WithNumber("limit",
			mcp.Description(fmt.Sprintf("Max bytes to return. Default and max: %d. Ignored when head/tail set.", readFileLimit)),
			mcp.DefaultNumber(float64(readFileLimit)),
		),
		mcp.WithNumber("head",
			mcp.Description("Return the first N lines instead of byte ranges. Mutually exclusive with tail."),
		),
		mcp.WithNumber("tail",
			mcp.Description("Return the last N lines instead of byte ranges. Mutually exclusive with head."),
		),
	)
}

func handleReadFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	rawPath, ok := args["path"].(string)
	if !ok || rawPath == "" {
		return mcp.NewToolResultError("read_file: 'path' is required"), nil
	}
	path, err := validatePathCtx(ctx, rawPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read_file: %v", err)), nil
	}

	headN, hasHead := numArg(args, "head")
	tailN, hasTail := numArg(args, "tail")
	if hasHead && hasTail {
		return mcp.NewToolResultError("read_file: 'head' and 'tail' are mutually exclusive"), nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read_file: %v", err)), nil
	}
	if info.IsDir() {
		return mcp.NewToolResultError(fmt.Sprintf("read_file: %s is a directory (use list_directory)", path)), nil
	}

	if hasHead {
		return readFileHead(path, info, int(headN))
	}
	if hasTail {
		return readFileTail(path, info, int(tailN))
	}
	return readFileBytes(path, info, args)
