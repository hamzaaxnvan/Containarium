package sentinel

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
)

// /sentinel/primaries (GET/POST/DELETE) was the one route on the binary-server
// mux that let a network caller read the full primary registry, overwrite any
// pool's declared routing target, or delete it outright — no credential of
// any kind. These tests pin the fix the same way #1102 pinned /peer/: they
// exercise the REAL route table via buildBinaryServerMux, not a hand-rolled
// mux, so removing the middleware in binaryserver.go would fail them.

const testPrimariesSecret = "primaries-auth-test-secret-32-bytes!"

// newPrimariesTestServer serves the real binary-server route table over a
// bare Manager — /sentinel/primaries needs no backend, no key store, just
// the primary registry and the HMAC secret under test.
func newPrimariesTestServer(t *testing.T, secret string) *httptest.Server {
	t.Helper()
	m := &Manager{
		backends:   NewBackendPool(),
		primaries:  NewPrimaryRegistry(),
		hmacSecret: []byte(secret),
	}
	srv := httptest.NewServer(buildBinaryServerMux("/nonexistent/containarium", m))
	t.Cleanup(srv.Close)
	return srv
}

func doPrimaries(t *testing.T, srv *httptest.Server, method, path, secret string, signed bool, body io.Reader) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if signed {
		auth.SignSentinelRequest(req, []byte(secret))
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody)
}

