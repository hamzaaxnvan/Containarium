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

