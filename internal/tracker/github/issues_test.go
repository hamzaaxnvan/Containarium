package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestGetIssue_NormalizesFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/42":
			_, _ = w.Write([]byte(`{
				"number": 42, "title": "bug", "body": "it broke",
				"state": "open",
				"labels": [{"name": "bug"}, {"name": "p1"}],
				"assignee": {"login": "alice"}
			}`))
		case "/repos/acme/widgets/issues/42/comments":
			_, _ = w.Write([]byte(`[
				{"user": {"login": "bob"}, "created_at": "2027-01-01T00:00:00Z", "body": "looking into it"}
			]`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	a := New(nil)
	issue, err := a.GetIssue(context.Background(), tracker.Conn{BaseURL: srv.URL, Project: "acme/widgets", Credential: "ghp_x"}, 42)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Number != 42 || issue.Title != "bug" || issue.State != pb.TrackerIssueState_TRACKER_ISSUE_STATE_OPEN {
		t.Errorf("issue = %+v, want number=42 title=bug state=OPEN", issue)
	}
	if len(issue.Labels) != 2 || issue.Labels[0] != "bug" || issue.Labels[1] != "p1" {
		t.Errorf("Labels = %v, want [bug p1]", issue.Labels)
	}
	if issue.Assignee != "alice" {
		t.Errorf("Assignee = %q, want alice", issue.Assignee)
	}
	if len(issue.Comments) != 1 || issue.Comments[0].Author != "bob" || issue.Comments[0].Body != "looking into it" {
		t.Errorf("Comments = %+v, want one comment from bob", issue.Comments)
	}
}

func TestGetIssue_UnassignedAndClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		switch r.URL.Path {
		case "/repos/acme/widgets/issues/7":
			_, _ = w.Write([]byte(`{"number": 7, "title": "t", "body": "b", "state": "closed", "labels": [], "assignee": null}`))
		case "/repos/acme/widgets/issues/7/comments":
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	a := New(nil)
	issue, err := a.GetIssue(context.Background(), tracker.Conn{BaseURL: srv.URL, Project: "acme/widgets"}, 7)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Assignee != "" {
		t.Errorf("Assignee = %q, want empty (unassigned)", issue.Assignee)
	}
	if issue.State != pb.TrackerIssueState_TRACKER_ISSUE_STATE_CLOSED {
		t.Errorf("State = %v, want CLOSED", issue.State)
	}
	if issue.Comments != nil {
		t.Errorf("Comments = %v, want nil for an empty comment list", issue.Comments)
	}
}

func TestGetIssue_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	a := New(nil)
	_, err := a.GetIssue(context.Background(), tracker.Conn{BaseURL: srv.URL, Project: "acme/widgets"}, 999)
	if !errors.Is(err, tracker.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListIssues_FiltersOutPullRequests(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"number": 1, "title": "a real issue", "state": "open", "labels": []},
			{"number": 2, "title": "actually a PR", "state": "open", "labels": [], "pull_request": {}}
		]`))
	}))
	defer srv.Close()

	a := New(nil)
	issues, err := a.ListIssues(context.Background(), tracker.Conn{BaseURL: srv.URL, Project: "acme/widgets"}, tracker.IssueFilter{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Number != 1 {
		t.Fatalf("issues = %+v, want exactly issue #1 (PR #2 filtered out)", issues)
	}
	if gotQuery == "" || !strings.Contains(gotQuery, "state=all") {
		t.Errorf("query = %q, want state=all for an unfiltered list", gotQuery)
	}
}

