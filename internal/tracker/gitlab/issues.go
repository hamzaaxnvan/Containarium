package gitlab

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

type glAuthor struct {
	Username string `json:"username"`
}

// glIssue mirrors GET /projects/{id}/issues/{iid}. GitLab returns labels
// as plain strings (unlike GitHub's {name: "..."} objects).
type glIssue struct {
	IID         int64     `json:"iid"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	State       string    `json:"state"`
	Labels      []string  `json:"labels"`
	Assignee    *glAuthor `json:"assignee"`
}

// glNote mirrors one element of GET /projects/{id}/issues/{iid}/notes.
// System is true for GitLab's auto-generated activity notes ("assigned
// to @x", "changed the description") — those aren't comments and are
// filtered out.
type glNote struct {
	Author    glAuthor `json:"author"`
	CreatedAt string   `json:"created_at"`
	Body      string   `json:"body"`
	System    bool     `json:"system"`
}

// glPipeline is the subset of a pipeline object GetChange needs.
type glPipeline struct {
	Status string `json:"status"`
}

// glMergeRequest mirrors GET /projects/{id}/merge_requests/{iid}.
// HeadPipeline is null when no pipeline has ever run for this MR.
type glMergeRequest struct {
	IID             int64       `json:"iid"`
	State           string      `json:"state"`
	WebURL          string      `json:"web_url"`
	HeadPipeline    *glPipeline `json:"head_pipeline"`
	SourceBranch    string      `json:"source_branch"`
	SourceProjectID int64       `json:"source_project_id"`
	TargetProjectID int64       `json:"target_project_id"`
}

// GetIssue reads a single issue and its comments (two API calls: the
// issue itself, then its notes, filtered to non-system ones).
func (a *Adapter) GetIssue(ctx context.Context, conn tracker.Conn, number int64) (tracker.Issue, error) {
	apiBase := apiBaseURL(conn.BaseURL)
	projectPath := url.PathEscape(conn.Project)

	var raw glIssue
	if err := a.get(ctx, fmt.Sprintf("%s/projects/%s/issues/%d", apiBase, projectPath, number), conn.Credential, &raw); err != nil {
		return tracker.Issue{}, err
	}

	var notes []glNote
	if err := a.get(ctx, fmt.Sprintf("%s/projects/%s/issues/%d/notes", apiBase, projectPath, number), conn.Credential, &notes); err != nil {
		return tracker.Issue{}, fmt.Errorf("list notes: %w", err)
	}

	return toIssue(raw, notes), nil
}

// ListIssues enumerates issues. state/labels/search all map directly
// onto GitLab's own list query parameters — no separate search
// endpoint needed here, unlike GitHub.
func (a *Adapter) ListIssues(ctx context.Context, conn tracker.Conn, f tracker.IssueFilter) ([]tracker.Issue, error) {
	apiBase := apiBaseURL(conn.BaseURL)
	projectPath := url.PathEscape(conn.Project)

	q := url.Values{}
	switch f.State {
	case pb.TrackerIssueState_TRACKER_ISSUE_STATE_OPEN:
		q.Set("state", "opened")
	case pb.TrackerIssueState_TRACKER_ISSUE_STATE_CLOSED:
		q.Set("state", "closed")
		// UNSPECIFIED: omit state entirely — GitLab has no "all" state
		// value; omitting the parameter is what returns every state.
	}
	if len(f.Labels) > 0 {
		q.Set("labels", strings.Join(f.Labels, ","))
	}
	if f.Search != "" {
		q.Set("search", f.Search)
	}
	q.Set("per_page", "100")

	raw, err := getAllPages[glIssue](ctx, a, fmt.Sprintf("%s/projects/%s/issues?%s", apiBase, projectPath, q.Encode()), conn.Credential)
	if err != nil {
		return nil, err
	}
	out := make([]tracker.Issue, 0, len(raw))
