package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
)

// TestListIssues_StopsAtMaxListPages proves the pagination loop (#2040)
// is bounded: an upstream that always answers with a next link is read
// for exactly tracker.MaxListPages pages, and what was collected is
// returned rather than the call looping forever.
func TestListIssues_StopsAtMaxListPages(t *testing.T) {
	var requests int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/acme/widgets/issues?page=%d>; rel="next"`, srv.URL, n+1))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `[{"number": %d, "title": "t", "state": "open", "labels": []}]`, n)
	}))
