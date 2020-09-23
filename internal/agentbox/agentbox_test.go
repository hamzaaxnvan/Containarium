//go:build !windows

package agentbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/logframe"
	"github.com/mark3labs/mcp-go/mcp"
)

// callTool builds a CallToolRequest with the given args and runs the
// supplied handler. Returns the text from the first content block, plus
// the result for callers that want to assert the IsError flag.
func callTool(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]interface{}) (string, *mcp.CallToolResult) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("empty result")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("first content block is not text: %T", res.Content[0])
	}
	return tc.Text, res
}

// ----- shell_exec ------------------------------------------------------

func TestShellExec_Basic(t *testing.T) {
	out, _ := callTool(t, handleShellExec, map[string]interface{}{
		"command": "echo hello && echo bye >&2",
	})
	if !strings.Contains(out, "exit_code: 0") {
		t.Errorf("missing exit_code 0 in:\n%s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("stdout missing 'hello' in:\n%s", out)
	}
	if !strings.Contains(out, "bye") {
		t.Errorf("stderr missing 'bye' in:\n%s", out)
	}
}

func TestShellExec_NonZeroExit(t *testing.T) {
	out, _ := callTool(t, handleShellExec, map[string]interface{}{
		"command": "exit 7",
	})
	if !strings.Contains(out, "exit_code: 7") {
		t.Errorf("expected exit 7, got:\n%s", out)
	}
}

// TestShellExec_Timeout previously only checked the output for the word
// "timeout" — which let the command's exit be reported correctly (via
// ctx.Err()) even though the process itself wasn't actually killed early;
// /bin/sh -c "sleep 5" forks a genuine child rather than exec-replacing
// itself, so killing just the shell (exec.CommandContext's default) left
// the grandchild running and holding the stdout/stderr pipe open, and
// Wait() blocked for the full natural 5s before returning. Asserting
// elapsed time is what actually catches that class of bug; a bare
// string-in-output check does not, and silently didn't for a while.
func TestShellExec_Timeout(t *testing.T) {
	start := time.Now()
	out, _ := callTool(t, handleShellExec, map[string]interface{}{
		"command":         "sleep 5",
		"timeout_seconds": float64(1),
	})
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("took %s, want well under the 5s sleep (timeout should kill the whole command tree around 1s)", elapsed)
	}
	if !strings.Contains(out, "timeout") {
		t.Errorf("expected timeout marker in:\n%s", out)
	}
}

func TestShellExec_OutputTruncation(t *testing.T) {
	// Generate ~512 KiB of output so we cross the 256 KiB cap.
	out, _ := callTool(t, handleShellExec, map[string]interface{}{
		"command": "head -c 524288 /dev/urandom | base64 -w0 | head -c 524288",
	})
	if !strings.Contains(out, "output truncated") {
		t.Errorf("expected truncation marker in capped output, got len=%d", len(out))
	}
}

func TestShellExec_MissingCommand(t *testing.T) {
	_, res := callTool(t, handleShellExec, map[string]interface{}{})
	if !res.IsError {
		t.Errorf("expected IsError when command missing")
	}
}

// ----- read_file -------------------------------------------------------

func TestReadFile_Basic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(p, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := callTool(t, handleReadFile, map[string]interface{}{"path": p})
	if !strings.Contains(out, "bytes_returned: 11") {
		t.Errorf("wrong bytes_returned in:\n%s", out)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("missing content in:\n%s", out)
	}
	if !strings.Contains(out, "truncated: false") {
		t.Errorf("should not be truncated for 11-byte file:\n%s", out)
	}
}

func TestReadFile_OffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data")
	if err := os.WriteFile(p, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := callTool(t, handleReadFile, map[string]interface{}{
		"path":   p,
		"offset": float64(3),
		"limit":  float64(4),
	})
	if !strings.Contains(out, "3456") {
		t.Errorf("expected '3456' content, got:\n%s", out)
	}
	if !strings.Contains(out, "truncated: true") {
		t.Errorf("expected truncated=true:\n%s", out)
	}
}

func TestReadFile_RefusesDirectory(t *testing.T) {
	_, res := callTool(t, handleReadFile, map[string]interface{}{"path": t.TempDir()})
	if !res.IsError {
		t.Errorf("expected error reading a directory")
	}
}

func TestReadFile_NotFound(t *testing.T) {
	_, res := callTool(t, handleReadFile, map[string]interface{}{
		"path": "/nonexistent/path/please",
	})
	if !res.IsError {
		t.Errorf("expected error for missing file")
	}
}

// ----- write_file ------------------------------------------------------

func TestWriteFile_AtomicMkdirp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nested", "dir", "out.txt")
	out, _ := callTool(t, handleWriteFile, map[string]interface{}{
		"path":    p,
		"content": "atomic\nwrite",
		"mode":    "0600",
	})
	if !strings.Contains(out, "bytes_written: 12") {
		t.Errorf("wrong bytes_written in:\n%s", out)
	}
	read, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("file not at destination: %v", err)
	}
	if string(read) != "atomic\nwrite" {
		t.Errorf("content mismatch: %q", read)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestWriteFile_NoTempFileLeftOnSuccess(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x")
	_, _ = callTool(t, handleWriteFile, map[string]interface{}{
		"path": p, "content": "ok",
	})
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".agent-box.") {
			t.Errorf("temp file leaked: %s", e.Name())
		}
	}
}
