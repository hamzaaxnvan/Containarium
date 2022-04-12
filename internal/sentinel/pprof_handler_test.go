package sentinel

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
)

// #1352: the sentinel had no way to produce a heap profile, so the leak in
// #1349 was OOM-killed at 565 MB having never been profiled and could not be
// root-caused without shipping an instrumented build to production.
//
// These tests pin the two halves that matter: the profile is actually usable
// by `go tool pprof` (not just "some bytes came back"), and it is gated behind
// the ADMIN secret — a heap dump contains whatever the process holds in
// memory, which on a sentinel includes tunnel-join tokens and TLS key
// material, so it is strictly more sensitive than the cluster-wide daemon
// secret's blast radius.

const (
	testPprofAdminSecret = "pprof-admin-secret-at-least-32-bytes!!"
	testPprofHMACSecret  = "pprof-daemon-hmac-secret-32-bytes-long!"
)

// newPprofTestServer serves the REAL binary-server route table, for the same
// reason newPeerProxyTestServer does (#1102): which middleware a route is
// registered behind IS the fix here, so a hand-wired mux would keep these
// tests green through a revert of the gate.
func newPprofTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	m := &Manager{
		backends:    NewBackendPool(),
		adminSecret: []byte(testPprofAdminSecret),
		hmacSecret:  []byte(testPprofHMACSecret),
	}
	srv := httptest.NewServer(buildBinaryServerMux("/nonexistent/containarium", m))
	t.Cleanup(srv.Close)
	return srv
}

// getPprof issues a GET, signing with secret when it is non-empty.
func getPprof(t *testing.T, srv *httptest.Server, path, secret string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if secret != "" {
		auth.SignSentinelRequest(req, []byte(secret))
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, body
}

func TestPprofRequiresAdminSecret(t *testing.T) {
	srv := newPprofTestServer(t)

	// Unsigned — the state the pentest module (module_web.go) probes for.
	if code, _ := getPprof(t, srv, "/debug/pprof/heap", ""); code != http.StatusUnauthorized {
		t.Fatalf("unsigned /debug/pprof/heap = %d, want 401 — profile endpoint is exposed", code)
	}

	// Signed with the cluster-wide DAEMON secret. Every backend daemon holds
	// this one for keysync/certsync; a heap dump is a bigger capability than
	// that, so it must not be sufficient here.
	if code, _ := getPprof(t, srv, "/debug/pprof/heap", testPprofHMACSecret); code != http.StatusUnauthorized {
		t.Fatalf("/debug/pprof/heap signed with the daemon HMAC secret = %d, want 401 — "+
			"gated on the wrong secret, every daemon in the cluster can dump sentinel memory", code)
	}

	// Signed with the admin secret — the one legitimate caller.
	if code, _ := getPprof(t, srv, "/debug/pprof/heap", testPprofAdminSecret); code != http.StatusOK {
		t.Fatalf("/debug/pprof/heap signed with the admin secret = %d, want 200", code)
	}
}

// TestPprofHeapIsReadableByGoToolPprof is the acceptance criterion for #1352:
// not "bytes came back" but "the bytes are a profile go tool pprof can open,
// with the sample type you need to find a leak".
