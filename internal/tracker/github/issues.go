package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

var _ tracker.ReaderProvider = (*Adapter)(nil)

// ghUser is the subset of GitHub's user object every response below
// needs.
type ghUser struct {
	Login string `json:"login"`
}

type ghLabel struct {
	Name string `json:"name"`
}

// ghIssue mirrors GET /repos/{owner}/{repo}/issues/{number} and the
// list endpoint's array elements. PullRequest is non-nil when this
// "issue" is actually a pull request — GitHub's issues endpoints return
// both; ListIssues filters these out so it returns issues only, GetIssue
// is never called with a PR's number by a correct caller.
type ghIssue struct {
	Number      int64     `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	Labels      []ghLabel `json:"labels"`
	Assignee    *ghUser   `json:"assignee"`
	PullRequest *ghPRStub `json:"pull_request"`
}

// ghPRStub's presence (not its contents) is the signal GetIssue/ListIssues need.
type ghPRStub struct{}

type ghComment struct {
	User      ghUser `json:"user"`
	CreatedAt string `json:"created_at"`
	Body      string `json:"body"`
}

// ghPull mirrors GET /repos/{owner}/{repo}/pulls/{number}.
type ghPull struct {
	Number  int64  `json:"number"`
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
}

// ghCombinedStatus mirrors GET /repos/{owner}/{repo}/commits/{ref}/status.
type ghCombinedStatus struct {
	State      string `json:"state"`
	TotalCount int    `json:"total_count"`
}

// GetIssue reads a single issue and its comments (two API calls: the
// issue itself, then its comments — GitHub has no single endpoint that
// returns both).
func (a *Adapter) GetIssue(ctx context.Context, conn tracker.Conn, number int64) (tracker.Issue, error) {
	base := apiBase(conn.BaseURL)

	var raw ghIssue
	if err := a.get(ctx, fmt.Sprintf("%s/repos/%s/issues/%d", base, conn.Project, number), conn.Credential, &raw); err != nil {
		return tracker.Issue{}, err
	}

	var comments []ghComment
	if err := a.get(ctx, fmt.Sprintf("%s/repos/%s/issues/%d/comments", base, conn.Project, number), conn.Credential, &comments); err != nil {
		return tracker.Issue{}, fmt.Errorf("list comments: %w", err)
	}

	return toIssue(raw, comments), nil
}

// ListIssues enumerates issues. Search text routes through GitHub's
// Search API (a different endpoint and response shape from the plain
// list); everything else uses the ordinary issues-list endpoint.
// Either way, entries with a non-nil PullRequest are dropped — GitHub's
// issues endpoints return pull requests too, and ListTrackerIssues must
// return issues only.
func (a *Adapter) ListIssues(ctx context.Context, conn tracker.Conn, f tracker.IssueFilter) ([]tracker.Issue, error) {
	base := apiBase(conn.BaseURL)

	if f.Search != "" {
		return a.searchIssues(ctx, base, conn, f)
	}

	q := url.Values{}
	switch f.State {
	case pb.TrackerIssueState_TRACKER_ISSUE_STATE_OPEN:
		q.Set("state", "open")
	case pb.TrackerIssueState_TRACKER_ISSUE_STATE_CLOSED:
		q.Set("state", "closed")
	default:
		q.Set("state", "all")
	}
	if len(f.Labels) > 0 {
		q.Set("labels", strings.Join(f.Labels, ","))
	}
	q.Set("per_page", "100")

	raw, err := getAllPages(ctx, a, fmt.Sprintf("%s/repos/%s/issues?%s", base, conn.Project, q.Encode()), conn.Credential,
		func(page []ghIssue) []ghIssue { return page })
	if err != nil {
		return nil, err
	}
	return toIssues(raw), nil
}

// ghSearchPage mirrors one page of GET /search/issues.
type ghSearchPage struct {
	Items []ghIssue `json:"items"`
