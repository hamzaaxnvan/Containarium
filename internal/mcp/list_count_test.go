package mcp

// #1214: list_containers printed "Found 0 container(s):" and then listed the
// whole fleet. Cosmetic for a human, who sees the boxes underneath. Not
// cosmetic for an agent: the count is the first token in the response and
// directly contradicts the payload, and an agent that concludes a backend is
// empty — and therefore safe to wipe — is making the "orphan" inference that
// has already deleted a live container here once.
//
// The summary line is now derived from the rendered list, so it cannot
// disagree with it whatever the daemon reports.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noTotalCount tells listServer to omit the totalCount field entirely, the
// way a daemon that never populates it would.
const noTotalCount = -1

// listServer returns a stub daemon emitting n containers, with whatever
// totalCount the caller wants to claim.
func listServer(t *testing.T, n int, claimedTotal int) *httptest.Server {
	t.Helper()
	var boxes []string
	for i := 0; i < n; i++ {
		boxes = append(boxes, fmt.Sprintf(
			`{"name":"box%d","username":"cld-%d","state":"CONTAINER_STATE_RUNNING"}`, i, i))
	}
	total := fmt.Sprintf(`,"totalCount":%d`, claimedTotal)
	if claimedTotal == noTotalCount {
		total = ""
	}
	body := fmt.Sprintf(`{"containers":[%s]%s}`, strings.Join(boxes, ","), total)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// countRendered counts the container entries actually printed.
func countRendered(out string) int {
	return strings.Count(out, "📦 ")
}

// summaryCount extracts N from "Found N container(s):".
func summaryCount(t *testing.T, out string) int {
	t.Helper()
	const prefix = "Found "
	i := strings.Index(out, prefix)
	require.GreaterOrEqual(t, i, 0, "output has no summary line:\n%s", out)
