package sentinel

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
)

// #1102: /peer/ was the one route on the binary-server mux with no
// authentication, while every neighbouring /sentinel/* route is HMAC
// gated. These tests pin both halves of the fix — the gate itself, and
// the uniform rejection that stops an authorized caller from using the
// route to enumerate which backends a sentinel fronts.

const testAdminSecret = "peer-proxy-admin-secret-32-bytes-long!"

// newPeerProxyTestServer serves the REAL binary-server route table over
// a manager holding one backend that points at a stub daemon.
//
// It deliberately calls buildBinaryServerMux rather than registering
// /peer/ itself. An earlier version of this file built its own mux, and
// mutation testing showed the consequence: removing the HMAC middleware
// from binaryserver.go entirely — i.e. reverting the whole fix — left
// every test in this file green, because they were exercising a route
// table the test had wired correctly by hand. Which middleware a route
// is registered behind IS the fix for #1102, so it has to be the real
// registration under test.
func newPeerProxyTestServer(t *testing.T, adminSecret string) (*httptest.Server, string) {
	t.Helper()

	// Stub daemon standing in for the backend's health-port listener.
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "daemon saw "+r.URL.Path)
	}))
	t.Cleanup(daemon.Close)

	du, err := url.Parse(daemon.URL)
	if err != nil {
		t.Fatalf("parse daemon url: %v", err)
	}
	port, err := strconv.Atoi(du.Port())
	if err != nil {
		t.Fatalf("daemon port: %v", err)
	}

	m := &Manager{
		backends:    NewBackendPool(),
		adminSecret: []byte(adminSecret),
	}
	m.config.HealthPort = port
	m.backends.Add(&Backend{ID: "tunnel-known-1", Type: BackendTunnel, IP: du.Hostname()})

	// binaryPath only feeds the /containarium download routes, which these
	// tests never call; a path that doesn't exist is fine here (StartBinaryServer
	// is what stats it, and we deliberately don't go through that — it binds a
	// real port and requires the installed binary).
	srv := httptest.NewServer(buildBinaryServerMux("/nonexistent/containarium", m))
	t.Cleanup(srv.Close)
	return srv, daemon.URL
}

// doPeer issues a request to path, signing it with secret when signed.
func doPeer(t *testing.T, srv *httptest.Server, path, secret string, signed bool) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if signed {
		auth.SignSentinelRequest(req, []byte(secret))
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// TestPeerProxy_UnsignedRequestRejected is the core of #1102: this route
// used to serve anyone who could reach the port.
func TestPeerProxy_UnsignedRequestRejected(t *testing.T) {
	srv, _ := newPeerProxyTestServer(t, testAdminSecret)

	code, _ := doPeer(t, srv, "/peer/tunnel-known-1/v1/containers", "", false)
	if code != http.StatusUnauthorized {
		t.Errorf("unsigned /peer/ = %d, want 401 — the route must not be reachable unauthenticated", code)
	}
}

// TestPeerProxy_WrongSecretRejected: holding *a* sentinel secret is not
// enough. The daemon-wide HMAC secret is distributed to every backend
// host; the admin secret is the control plane's. Gating on the wrong one
// would leave the route reachable from any host in the fleet, which is
// exactly the population #1102 is worried about.
func TestPeerProxy_WrongSecretRejected(t *testing.T) {
	srv, _ := newPeerProxyTestServer(t, testAdminSecret)

	code, _ := doPeer(t, srv, "/peer/tunnel-known-1/v1/containers", "a-different-secret-of-sufficient-length", true)
	if code != http.StatusUnauthorized {
		t.Errorf("wrongly-signed /peer/ = %d, want 401", code)
	}
}

// TestPeerProxy_SignedRequestProxies is the control. Without it every
// assertion here would pass on a route that rejects everything.
func TestPeerProxy_SignedRequestProxies(t *testing.T) {
