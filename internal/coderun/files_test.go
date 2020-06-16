package coderun

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// scriptedConn answers each tool call from a per-tool canned reply and records
// the arguments it was handed, so a test can assert on what `code run` actually
// asked the box to do.
type scriptedConn struct {
	replies map[string]string
	errs    map[string]error
	calls   []scriptedCall
}

type scriptedCall struct {
	tool string
	args map[string]any
}

func (c *scriptedConn) Initialize(context.Context, mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	return &mcp.InitializeResult{}, nil
}

func (c *scriptedConn) CallTool(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.Params.Name
	args, _ := req.Params.Arguments.(map[string]any)
	c.calls = append(c.calls, scriptedCall{tool: name, args: args})
	if err, ok := c.errs[name]; ok {
		return nil, err
	}
	reply, ok := c.replies[name]
	if !ok {
		return nil, errors.New("scriptedConn: no reply for " + name)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Type: "text", Text: reply}}}, nil
}

func (c *scriptedConn) Close() error { return nil }

func (c *scriptedConn) call(tool string) (map[string]any, bool) {
	for _, got := range c.calls {
		if got.tool == tool {
			return got.args, true
		}
	}
	return nil, false
}

// sessionWith builds a Session over conn without any ssh involved.
func sessionWith(conn mcpConn) *Session {
	return &Session{mcp: conn}
}

func TestExpandHome(t *testing.T) {
	tests := []struct{ in, want string }{
		{"$HOME/.pi/gateway.env", "/home/alice/.pi/gateway.env"},
		{"$HOME/.containarium/code.json", "/home/alice/.containarium/code.json"},
