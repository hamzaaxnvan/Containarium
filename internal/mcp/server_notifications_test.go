package mcp

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #2529. A message with no "id" is a JSON-RPC notification, and the server must
// not reply to one. It used to answer notifications/initialized — sent on every
// MCP connection — with an error whose id is null, which Claude Code's strict
// client rejects, dropping the connection; the agent then had no tools.

func newNotificationTestServer(t *testing.T, token string) *Server {
	t.Helper()
	s, err := NewServer(&Config{ServerURL: "http://localhost:8080", JWTToken: token})
	require.NoError(t, err)
	return s
}

func TestHandleLine_NotificationsGetNoReply(t *testing.T) {
	s := newNotificationTestServer(t, "test-token")
	for _, line := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`,
		`{"jsonrpc":"2.0","method":"notifications/some/future/one"}`,
		// Not a notification METHOD, but a message with no id is a notification
		// all the same: it must not be executed or answered.
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"list_containers"}}`,
		`{"jsonrpc":"2.0","method":"does/not/exist"}`,
	} {
		resp, reply := s.handleLine([]byte(line))
		assert.False(t, reply, "no reply to %s", line)
		assert.Nil(t, resp, line)
	}
}

func TestHandleLine_RequestsStillGetReplies(t *testing.T) {
	s := newNotificationTestServer(t, "test-token")

	resp, reply := s.handleLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	require.True(t, reply)
	assert.Equal(t, float64(1), resp.ID)
	assert.Nil(t, resp.Error)

	// An unknown METHOD with an id is still -32601, echoing the id.
	resp, reply = s.handleLine([]byte(`{"jsonrpc":"2.0","id":"abc","method":"does/not/exist"}`))
	require.True(t, reply)
