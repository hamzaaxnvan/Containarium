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
