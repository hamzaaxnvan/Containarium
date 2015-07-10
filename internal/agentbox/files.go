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
}

func readFileBytes(path string, info os.FileInfo, args map[string]interface{}) (*mcp.CallToolResult, error) {
	offset := int64(0)
	if v, ok := args["offset"].(float64); ok && v >= 0 {
		offset = int64(v)
	}
	limit := int64(readFileLimit)
	if v, ok := args["limit"].(float64); ok && v > 0 && int64(v) < readFileLimit {
		limit = int64(v)
	}

	// #nosec G304 -- path was sanitized by validatePath; agent-box's contract
	// is to read agent-supplied paths, optionally constrained by AGENTBOX_ROOT.
	f, err := os.Open(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read_file: %v", err)), nil
	}
	defer func() { _ = f.Close() }()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("read_file: seek to %d: %v", offset, err)), nil
		}
	}

	buf := make([]byte, limit)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return mcp.NewToolResultError(fmt.Sprintf("read_file: %v", err)), nil
	}
	body := fmt.Sprintf(
		"path: %s\nsize: %d\noffset: %d\nbytes_returned: %d\ntruncated: %v\n--- content ---\n%s",
		path, info.Size(), offset, n, int64(n) < info.Size()-offset, string(buf[:n]),
	)
	return mcp.NewToolResultText(body), nil
}

func readFileHead(path string, info os.FileInfo, n int) (*mcp.CallToolResult, error) {
	if n <= 0 {
		return mcp.NewToolResultError("read_file: 'head' must be > 0"), nil
	}
	// #nosec G304 -- see read_file rationale; path is validatePath-sanitized.
	f, err := os.Open(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read_file: %v", err)), nil
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // 1 MiB max line
	var b strings.Builder
	read, byteCount := 0, 0
	for scanner.Scan() && read < n && byteCount < readFileLimit {
		line := scanner.Text()
		b.WriteString(line)
		b.WriteByte('\n')
		read++
		byteCount += len(line) + 1
	}
	if err := scanner.Err(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read_file: head scan: %v", err)), nil
	}
	body := fmt.Sprintf(
		"path: %s\nsize: %d\nmode: head\nlines_returned: %d\ntruncated: %v\n--- content ---\n%s",
		path, info.Size(), read, byteCount >= readFileLimit, b.String(),
	)
	return mcp.NewToolResultText(body), nil
}

func readFileTail(path string, info os.FileInfo, n int) (*mcp.CallToolResult, error) {
	if n <= 0 {
		return mcp.NewToolResultError("read_file: 'tail' must be > 0"), nil
	}
	// Simple impl: ring buffer of strings while scanning. Adequate for log
	// files where N is small (<10k); a chunked-from-end seek would be
	// faster on huge files but adds complexity we don't need yet.
	// #nosec G304 -- see read_file rationale; path is validatePath-sanitized.
	f, err := os.Open(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read_file: %v", err)), nil
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	ring := make([]string, 0, n)
	for scanner.Scan() {
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read_file: tail scan: %v", err)), nil
	}
	var b strings.Builder
	byteCount := 0
