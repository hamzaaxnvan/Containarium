package mcp

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/coderun"
)

// code_answer is offered only to a token that names code:answer (#3191),
// the scope AnswerRunInput requires; ssh:write alone does not reach it.
func TestCodeAnswerTool_RequiresTheAnswerScope(t *testing.T) {
	srv := &Server{}
	srv.registerTools()
	var tool *Tool
	for i := range srv.tools {
		if srv.tools[i].Name == "code_answer" {
			tool = &srv.tools[i]
		}
	}
	require.NotNil(t, tool, "code_answer is not registered")
	assert.Equal(t, auth.ScopeCodeAnswer, tool.RequiredScope)
	assert.False(t, toolAllowed([]string{auth.ScopeSSHWrite}, tool), "an ssh:write-only token reached code_answer")
}

// code_answer reads the request's prompt, answers bound to it over the same
// REST paths `code answer --http` uses, and reports the box's response.
func TestHandleCodeAnswer_BindsTheListedPrompt(t *testing.T) {
	var answered map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/containers/alice/code-runs/task/inputs":
			_, _ = w.Write([]byte(`{"running":true,"open":[{"requestId":"req-0123456789abcdef","tool":"Bash","prompt":"Bash: make deploy","input":"{\"command\":\"make deploy\"}"}]}`))
		case "/v1/containers/alice/code-runs/task/inputs/req-0123456789abcdef:answer":
			assert.Equal(t, http.MethodPost, r.Method)
			b, _ := io.ReadAll(r.Body)
			require.NoError(t, json.Unmarshal(b, &answered))
			_, _ = w.Write([]byte(`{"outcome":"RUN_INPUT_ANSWER_OUTCOME_ANSWERED","closedAnswer":"RUN_INPUT_ANSWER_DENY","closeReason":"RUN_INPUT_CLOSE_REASON_ANSWERED"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
