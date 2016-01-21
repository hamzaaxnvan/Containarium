package mcp

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// code_runs lists a box's run records over the same REST path
// `containarium code runs --http` uses, formatted by the same function.
func TestHandleCodeRuns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/containers/alice/code-runs", r.URL.Path)
		_, _ = w.Write([]byte(`{"runs":[{"runName":"task","startedAt":"2026-09-28T10:00:00Z",` +
			`"endedAt":"2026-09-28T10:01:00Z","exitCode":0,"outcome":"BOX_RUN_OUTCOME_EXITED",` +
			`"logPath":"/tmp/agent-box/task.log","captureMode":"CAPTURE_MODE_FRAMED"}]}`))
	}))
	defer server.Close()

	out, err := handleCodeRuns(NewClient(server.URL, "test-token"), map[string]interface{}{"box": "alice"})
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 2, out)
	assert.Equal(t, []string{"task", "exited", "0", "2026-09-28T10:00:00Z", "2026-09-28T10:01:00Z", "-", "/tmp/agent-box/task.log"},
		strings.Fields(lines[1]))
}
