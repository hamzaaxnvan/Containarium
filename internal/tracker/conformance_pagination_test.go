package tracker_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
	trackergithub "github.com/footprintai/containarium/internal/tracker/github"
	trackergitlab "github.com/footprintai/containarium/internal/tracker/gitlab"
)

// paginatedIssueCount is deliberately more than two full pages of 100,
// so an adapter that reads only the first page (#2040) sees 100 of them
// and one that stops after two sees 200.
const paginatedIssueCount = 250

// TestProviderConformance_ListIssuesFollowsPagination proves both
// adapters return every issue when the upstream splits the list over
// several pages linked by an RFC 8288 Link header (rel="next") — the
// mechanism both GitHub and GitLab use. Before #2040 each adapter read a
// single page of 100, so an older issue carrying a routed scope label
// was never seen by the dispatcher.
func TestProviderConformance_ListIssuesFollowsPagination(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider tracker.Provider
		baseURL  string
	}{
		{"github", trackergithub.New(nil), newPaginatedFixture(t, "/repos/acme/widgets/issues", "", githubIssueJSON)},
		{"gitlab", trackergitlab.New(nil), newPaginatedFixture(t, "/api/v4/projects/acme%2Fwidgets/issues", "", gitlabIssueJSON)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issues, err := tc.provider.ListIssues(context.Background(), tracker.Conn{BaseURL: tc.baseURL, Project: "acme/widgets"}, tracker.IssueFilter{Labels: []string{"scope:engineer"}})
			if err != nil {
				t.Fatalf("ListIssues: %v", err)
			}
			if len(issues) != paginatedIssueCount {
				t.Fatalf("%s ListIssues returned %d issues, want all %d across 3 pages", tc.name, len(issues), paginatedIssueCount)
			}
			seen := make(map[int64]bool, len(issues))
			for _, is := range issues {
				seen[is.Number] = true
			}
			for n := int64(1); n <= paginatedIssueCount; n++ {
				if !seen[n] {
					t.Fatalf("%s ListIssues missing issue #%d", tc.name, n)
				}
			}
		})
	}
}

// TestProviderConformance_ListIssuesRefusesCrossHostNextLink proves a
// Link header pointing at a different host is not followed: the adapter
// would otherwise send the tracker credential to whatever host the
// upstream response names. The next link targets a second, live server
// that counts every request it receives, so the test proves the
// credential never left — not merely that some error mentioning "host"
// came back (a DNS failure for an unresolvable decoy host would satisfy
// that too, even with the guard removed).
func TestProviderConformance_ListIssuesRefusesCrossHostNextLink(t *testing.T) {
	for _, tc := range []struct {
		name        string
		provider    tracker.Provider
		escapedPath string
		issueJSON   func(int) string
	}{
