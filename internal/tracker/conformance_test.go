package tracker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/tracker"
	trackergithub "github.com/footprintai/containarium/internal/tracker/github"
	trackergitlab "github.com/footprintai/containarium/internal/tracker/gitlab"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestProviderConformance is the test the design note names to keep
// "provider-neutral" true: one suite, run against both adapters via the
// shared tracker.ReaderProvider interface, over fixtures built to
// represent the SAME logical issue/change on each provider's own wire
// shape. If the two adapters ever normalize an equivalent upstream
// response into different Go values, this fails — the whole point of
// the ReaderProvider contract is that a caller cannot tell which
// provider it's talking to from the shape of the answer.
func TestProviderConformance(t *testing.T) {
	fixedTime := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name     string
		provider tracker.Provider
		conn     tracker.Conn
	}{
		{"github", trackergithub.New(nil), tracker.Conn{BaseURL: newGitHubFixture(t, fixedTime), Project: "acme/widgets"}},
		{"gitlab", trackergitlab.New(nil), tracker.Conn{BaseURL: newGitLabFixture(t, fixedTime), Project: "acme/widgets"}},
	} {
		t.Run(tc.name+"/GetIssue", func(t *testing.T) {
			issue, err := tc.provider.GetIssue(context.Background(), tc.conn, 1)
			if err != nil {
				t.Fatalf("GetIssue: %v", err)
			}
			want := tracker.Issue{
				Number:   1,
				Title:    "conformance issue",
				Body:     "same body on every provider",
				State:    pb.TrackerIssueState_TRACKER_ISSUE_STATE_OPEN,
				Labels:   []string{"bug"},
				Assignee: "agent",
				Comments: []tracker.Comment{
					{Author: "reviewer", CreatedAt: fixedTime, Body: "lgtm"},
				},
			}
			if !reflect.DeepEqual(issue, want) {
				t.Errorf("%s GetIssue = %+v, want %+v", tc.name, issue, want)
			}
		})

		t.Run(tc.name+"/ListIssues_excludes changes", func(t *testing.T) {
			issues, err := tc.provider.ListIssues(context.Background(), tc.conn, tracker.IssueFilter{})
			if err != nil {
				t.Fatalf("ListIssues: %v", err)
			}
			if len(issues) != 1 || issues[0].Number != 1 {
				t.Errorf("%s ListIssues = %+v, want exactly issue #1 (the change-request-shaped entry excluded)", tc.name, issues)
			}
		})

		t.Run(tc.name+"/GetChange", func(t *testing.T) {
			change, err := tc.provider.GetChange(context.Background(), tc.conn, 2)
			if err != nil {
				t.Fatalf("GetChange: %v", err)
			}
			// URL is legitimately provider-specific — not compared.
			if change.Number != 2 {
				t.Errorf("%s GetChange.Number = %d, want 2", tc.name, change.Number)
			}
			if change.State != pb.TrackerIssueState_TRACKER_ISSUE_STATE_MERGED {
				t.Errorf("%s GetChange.State = %v, want MERGED", tc.name, change.State)
			}
			if change.CIVerdict != pb.TrackerCiVerdict_TRACKER_CI_VERDICT_SUCCESS {
				t.Errorf("%s GetChange.CIVerdict = %v, want SUCCESS", tc.name, change.CIVerdict)
			}
		})

		t.Run(tc.name+"/Comment", func(t *testing.T) {
			comment, err := tc.provider.Comment(context.Background(), tc.conn, 3, "conformance comment")
			if err != nil {
				t.Fatalf("Comment: %v", err)
			}
			if comment.Author != "reviewer" || comment.Body != "conformance comment" {
				t.Errorf("%s Comment = %+v, want author=reviewer body=%q", tc.name, comment, "conformance comment")
