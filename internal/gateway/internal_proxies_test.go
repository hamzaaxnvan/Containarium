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

