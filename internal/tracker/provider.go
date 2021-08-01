package tracker

import (
	"context"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Issue is the provider-neutral shape of a tracker issue — one `number`
// rather than GitHub's number vs GitLab's iid, a typed TrackerIssueState
// rather than "open"/"opened", normalized fields throughout. No
// provider-specific field name or string reaches a caller.
type Issue struct {
	Number int64
	Title  string
	Body   string
	State  pb.TrackerIssueState
	Labels []string
	// Assignee is the provider username, empty when unassigned. Single
	// value even on GitHub (which allows several) — the broker only
	// ever needs "is this claimed," and GitLab Free's single-assignee
	// model is what AssignIfUnassigned (#1922 step 5) is designed
	// around; a second GitHub assignee is invisible here by design
	// rather than by omission.
	Assignee string
	// Comments is populated only by GetIssue — ListIssues never fetches
	// them (neither provider's list endpoint includes them, and
	// fetching per-issue would be an N+1 call pattern neither
	// provider's rate limits are generous with).
	Comments []Comment
}

// Comment is one normalized issue or merge-request comment.
type Comment struct {
	Author    string
	CreatedAt time.Time
	Body      string
}

// Change is the provider-neutral shape of a GitHub pull request or
// GitLab merge request.
type Change struct {
	Number    int64
	State     pb.TrackerIssueState
	CIVerdict pb.TrackerCiVerdict
	URL       string
	// Branch is the branch the change's head points at (#1923). OpenChange
	// echoes the HeadBranch the caller told it to use; GetChange and
	// FindOpenChange report the provider's head/source branch (#3288).
	Branch string
}

// IssueFilter narrows ListIssues. The zero value matches every issue.
type IssueFilter struct {
	// State filters by issue state; UNSPECIFIED matches any state.
	State pb.TrackerIssueState
	// Labels, when non-empty, requires ALL of these labels (both
	// providers' native list APIs support this as an AND filter).
	Labels []string
	// Search is a free-text query against title/body, passed through to
	// the provider's own search — no normalization possible across two
	// different search implementations, so treat results as
	// best-effort, not authoritative.
	Search string
}

// OpenChangeRequest is #1923's submit-path input. Declared here now so
// the full Provider interface (below) is complete and both adapters
// satisfy it from the day they exist, even though nothing calls
// OpenChange until #1923 wires the submit path that builds one.
type OpenChangeRequest struct {
	HeadBranch  string
	BaseBranch  string
	Title       string
	Description string
	Draft       bool
}
