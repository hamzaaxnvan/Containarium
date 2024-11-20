package mcp

// Tests for #1922's tracker MCP tools' underlying *Client methods —
// wire-format shape (path, method, body, path-escaping) against an
// httptest server, mirroring collaborator_tools_test.go's pattern.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPClient_GetTrackerIssue(t *testing.T) {
	var sawPath, sawMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath, sawMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"issue":{"number":"7","title":"bug","body":"it broke","state":"TRACKER_ISSUE_STATE_OPEN","labels":["bug"],"assignee":"agent","comments":[{"author":"reviewer","createdAt":"2027-01-01T00:00:00Z","body":"looking"}]}}`)
	}))
	defer server.Close()

	c := NewClient(server.URL, "tok")
	issue, err := c.GetTrackerIssue(GetTrackerIssueRequest{Username: "alice", Connection: "default", Number: 7})
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, sawMethod)
	assert.Equal(t, "/v1/tracker/connections/alice/default/issues/7", sawPath)
	assert.Equal(t, int64(7), issue.Number)
	assert.Equal(t, "bug", issue.Title)
	assert.Equal(t, "agent", issue.Assignee)
	require.Len(t, issue.Comments, 1)
	assert.Equal(t, "reviewer", issue.Comments[0].Author)
}

func TestMCPClient_GetTrackerIssue_PathEscapesConnectionName(t *testing.T) {
	var sawPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"issue":{"number":"1","title":"t","body":"","state":"TRACKER_ISSUE_STATE_OPEN"}}`)
	}))
	defer server.Close()

	c := NewClient(server.URL, "tok")
	_, err := c.GetTrackerIssue(GetTrackerIssueRequest{Username: "alice", Connection: "a/b", Number: 1})
	require.NoError(t, err)
	assert.Equal(t, "/v1/tracker/connections/alice/a%2Fb/issues/1", sawPath)
}

func TestMCPClient_ListTrackerIssues_FiltersAsQueryParams(t *testing.T) {
	var sawQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"issues":[{"number":"1","title":"t","body":"","state":"TRACKER_ISSUE_STATE_OPEN"}]}`)
	}))
	defer server.Close()

	c := NewClient(server.URL, "tok")
	issues, err := c.ListTrackerIssues(ListTrackerIssuesRequest{
		Username: "alice", Connection: "default", State: "open", Labels: []string{"bug"}, Search: "crash",
	})
	require.NoError(t, err)
	require.Len(t, issues, 1)

	q, err := url.ParseQuery(sawQuery)
	require.NoError(t, err)
	assert.Equal(t, []string{"TRACKER_ISSUE_STATE_OPEN"}, q["state"])
	assert.Equal(t, []string{"bug"}, q["labels"])
	assert.Equal(t, []string{"crash"}, q["search"])
}

func TestMCPClient_GetTrackerChange(t *testing.T) {
	var sawPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"change":{"number":"2","state":"TRACKER_ISSUE_STATE_MERGED","ciVerdict":"TRACKER_CI_VERDICT_SUCCESS","url":"https://example.com/pr/2"}}`)
	}))
	defer server.Close()

	c := NewClient(server.URL, "tok")
	change, err := c.GetTrackerChange(GetTrackerChangeRequest{Username: "alice", Connection: "default", Number: 2})
	require.NoError(t, err)
	assert.Equal(t, "/v1/tracker/connections/alice/default/changes/2", sawPath)
	assert.Equal(t, int64(2), change.Number)
	assert.Equal(t, "TRACKER_ISSUE_STATE_MERGED", change.State)
	assert.Equal(t, "TRACKER_CI_VERDICT_SUCCESS", change.CiVerdict)
}

func TestMCPClient_CommentOnTrackerIssue(t *testing.T) {
	var sawPath, sawMethod string
	var sawBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath, sawMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&sawBody)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"comment":{"author":"agent-bot","createdAt":"2027-01-01T00:00:00Z","body":"hello"}}`)
	}))
	defer server.Close()

	c := NewClient(server.URL, "tok")
	comment, err := c.CommentOnTrackerIssue(CommentOnTrackerIssueRequest{
		Username: "alice", Connection: "default", Number: 7, Body: "hello",
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, sawMethod)
	assert.Equal(t, "/v1/tracker/connections/alice/default/issues/7/comments", sawPath)
