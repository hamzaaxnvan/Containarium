package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
)

// Phase 1.10 — /grafana/, /alertmanager/, /guacamole/ reverse
// proxies must require a valid JWT before forwarding. Each
// backend still enforces its own login, but the daemon's token
// is the floor of trust regardless. Audit finding A-MED-6.

const proxyTestSecret = "a-secret-that-is-at-least-32-bytes-long!!"

// newProxyTestGateway builds a GatewayServer with all three
// internal proxies pointing at a single recording backend. The
// backend records every Path it actually receives so we can
// assert auth gating (no record means the auth layer rejected
// the request before forwarding).
func newProxyTestGateway(t *testing.T) (*GatewayServer, *recordingBackend, string) {
	t.Helper()
	backend := &recordingBackend{
		srv: httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Capture the path the backend saw.
			backendPath = r.URL.Path
			_, _ = io.WriteString(w, "backend-ok:"+r.URL.Path)
		})),
	}
	t.Cleanup(backend.srv.Close)

	tm, err := auth.NewTokenManager(proxyTestSecret, "containarium")
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	tok, err := tm.GenerateToken("alice", []string{"admin"}, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	gs := &GatewayServer{
		authMiddleware:         auth.NewAuthMiddleware(tm),
		grafanaBackendURL:      backend.srv.URL,
		alertmanagerBackendURL: backend.srv.URL,
		guacamoleBackendURL:    backend.srv.URL,
	}
	return gs, backend, tok
}

type recordingBackend struct {
	srv *httptest.Server
}

// backendPath is set by the test backend on every request. The
// helper resets it between cases.
var backendPath string

// buildProxyMux wires the three reverse proxies onto a fresh mux
// using the same code path Start() uses. Extracted so tests
// don't need to spin up the full HTTP server.
func buildProxyMux(t *testing.T, gs *GatewayServer) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mountInternalProxies(mux, gs)
	return mux
}

func TestInternalProxies_RejectUnauthenticated(t *testing.T) {
