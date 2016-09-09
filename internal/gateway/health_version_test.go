package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/footprintai/containarium/pkg/version"
)

// #1647: a running host exposed no version anywhere. /health answered
// {"status":"healthy"} and nothing else; /version, /healthz and /metrics all
// 404'd; and `containarium backends versions` sat behind auth that returned
// 401 even from the primary itself. The only way to learn what a box was
// running was SSH plus `containarium version`.
//
// That made routine work harder than it should be: a fleet audit needed shell
// access to every machine, two production primaries silently diverged for
// weeks, and several filed bugs had to record "app version: unconfirmed"
// because the host could not be asked.
//
// /health is the natural home — it is already unauthenticated, already
// scraped, and already reached by anything doing liveness checks, so putting
// the version there makes every existing health check a version check too.

func TestHealthEndpoint_ReportsVersion(t *testing.T) {
	rec := httptest.NewRecorder()
	healthHandler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var got healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not valid JSON: %v (body: %s)", err, rec.Body.String())
	}

	if got.Version == "" {
		t.Error("version is empty — the whole point of #1647 is that a host can be asked " +
			"what it runs without shell access")
	}
	if got.Version != version.GetVersion() {
		t.Errorf("version = %q, want %q (the binary's own version)", got.Version, version.GetVersion())
	}
}

// The existing contract must not change: anything already parsing this
