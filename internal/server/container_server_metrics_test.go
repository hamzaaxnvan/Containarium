package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestGetMetrics_FleetWide_ReportsUnreachablePeerInsteadOfSilentlyDroppingIt
// (#1901): before this fix, a peer the sentinel marked unhealthy — even for
// one poll tick, e.g. mid-restart — contributed zero entries to the
// fleet-wide GetMetrics response with nothing but a server-side log line.
// The webui rendered that identically to "container has no usage yet",
// making a transient backend blip look like every container on that host
// had gone dark. The fix surfaces the skipped peer in
// `unreachable_backends` so callers can tell the two apart.
func TestGetMetrics_FleetWide_ReportsUnreachablePeerInsteadOfSilentlyDroppingIt(t *testing.T) {
	mock := incustest.NewMockBackend()

	pp := NewPeerPool("local-vm", "", nil, "")
	unhealthy := peerClientFor(httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unhealthy peer should never be dialed for metrics, got %s %s", r.Method, r.URL.Path)
	})))
