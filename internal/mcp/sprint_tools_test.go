package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sprintPresentBody = `{"mirror":{
  "umbrella":{"provider":"TRACKER_PROVIDER_GITHUB","project":"acme/app","number":"120"},
  "state":"SPRINT_MIRROR_STATE_PRESENT",
  "renderedMarkdown":"## Sprint 120\n- #121 ready",
  "board":{"project":"acme/app","umbrella":"120","umbrellaTitle":"Sprint 120","partial":true,"partialReason":"rate limited",
    "items":[{"number":"121","title":"First","state":"SPRINT_ITEM_STATE_READY","blockers":["119"],"openBlockers":["119"]}]}}}`

func sprintServer(t *testing.T, status int, body string, seen *http.Request) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = *r
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "tok")
}

func TestClient_GetSprintBoard_RequestAndDecode(t *testing.T) {
	var seen http.Request
	c := sprintServer(t, 200, sprintPresentBody, &seen)

	m, err := c.GetSprintBoard(GetSprintBoardRequest{OrgID: "org/1", Project: "acme/app", Number: 120})
	require.NoError(t, err)
	require.NotNil(t, m)

	assert.Equal(t, http.MethodGet, seen.Method)
	assert.Equal(t, "/v1/orgs/org%2F1/sprints/board", seen.URL.EscapedPath())
	q := seen.URL.Query()
	assert.Equal(t, "TRACKER_PROVIDER_GITHUB", q.Get("umbrella.provider"), "provider defaults to GitHub")
	assert.Equal(t, "acme/app", q.Get("umbrella.project"))
	assert.Equal(t, "120", q.Get("umbrella.number"))

	assert.Equal(t, SprintMirrorStatePresent, m.State)
	assert.Equal(t, "## Sprint 120\n- #121 ready", m.RenderedMarkdown)
	require.NotNil(t, m.Board)
	assert.Equal(t, int64(120), m.Board.Umbrella)
	assert.True(t, m.Board.Partial)
	require.Len(t, m.Board.Items, 1)
	assert.Equal(t, int64(121), m.Board.Items[0].Number)
	assert.Equal(t, []int64{119}, m.Board.Items[0].OpenBlockers)
}

func TestClient_ListSprints_RequestAndDecode(t *testing.T) {
	var seen http.Request
	c := sprintServer(t, 200, `{"sprints":[{"umbrella":{"provider":"TRACKER_PROVIDER_GITHUB","project":"acme/app","number":"120"},"title":"Sprint 120","state":"SPRINT_MIRROR_STATE_NO_MIRROR_YET"}]}`, &seen)

	got, err := c.ListSprints(ListSprintsRequest{OrgID: "org-1"})
	require.NoError(t, err)
	assert.Equal(t, "/v1/orgs/org-1/sprints", seen.URL.EscapedPath())
	require.Len(t, got, 1)
	assert.Equal(t, int64(120), got[0].Umbrella.Number)
	assert.Equal(t, SprintMirrorStateNoMirrorYet, got[0].State)
}

func TestHandleSprintBoard_Present(t *testing.T) {
	c := sprintServer(t, 200, sprintPresentBody, nil)
	out, err := handleSprintBoard(c, map[string]interface{}{"org_id": "o", "project": "acme/app", "number": float64(120)})
	require.NoError(t, err)
	assert.Contains(t, out, "## Sprint 120\n- #121 ready", "rendered markdown is passed through verbatim")
	assert.Contains(t, out, "PRESENT")
	assert.Contains(t, out, "partial")
	assert.Contains(t, out, "rate limited")
}

func TestHandleSprintBoard_NonErrorStates(t *testing.T) {
	for state, want := range map[string]string{
		"SPRINT_MIRROR_STATE_NO_MIRROR_YET":          "no board yet",
		"SPRINT_MIRROR_STATE_NOT_ENABLED":            "not enabled",
		"SPRINT_MIRROR_STATE_PROVIDER_NOT_SUPPORTED": "not supported",
	} {
		t.Run(state, func(t *testing.T) {
			c := sprintServer(t, 200, `{"mirror":{"state":"`+state+`"}}`, nil)
			out, err := handleSprintBoard(c, map[string]interface{}{"org_id": "o", "project": "acme/app", "number": float64(120)})
			require.NoError(t, err, "%s is a result, not an error", state)
			assert.Contains(t, out, want)
			assert.Contains(t, out, state[len("SPRINT_MIRROR_STATE_"):], "state name is surfaced")
		})
	}
}

func TestHandleSprintBoard_RejectedIsError(t *testing.T) {
	c := sprintServer(t, 200, `{"mirror":{"state":"SPRINT_MIRROR_STATE_REJECTED"}}`, nil)
	_, err := handleSprintBoard(c, map[string]interface{}{"org_id": "o", "project": "acme/app", "number": float64(120)})
	require.Error(t, err)
