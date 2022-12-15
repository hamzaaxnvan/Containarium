package sentinel

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// dialToTestServer builds a dial func that ignores spotID/port (the real
// DialTunnel routes by those; here we just need "the tunnel" to land on a
// local httptest server) and always connects to ts's listener — mirroring
// how a real tunnel client would forward to whatever's on the primary's
// local port, without needing a real yamux session in these tests.
func dialToTestServer(t *testing.T, ts *httptest.Server) func(spotID string, port int) (net.Conn, error) {
	t.Helper()
	addr := strings.TrimPrefix(strings.TrimPrefix(ts.URL, "https://"), "http://")
	return func(string, int) (net.Conn, error) {
		return net.Dial("tcp", addr)
	}
}

// hostRoutedServer starts a TLS test server whose handler mimics a
// misconfigured Caddy: it answers 200 for hosts in `ok`, and 502 (with a
// Caddy-branded body, matching the real symptom in #1872) for anything
// else.
func hostRoutedServer(t *testing.T, ok ...string) *httptest.Server {
	t.Helper()
	allowed := make(map[string]bool, len(ok))
	for _, h := range ok {
		allowed[h] = true
	}
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if allowed[r.Host] {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Server", "Caddy")
		w.WriteHeader(http.StatusBadGateway)
	}))
}

func TestProbeHostname_ReachableOn200(t *testing.T) {
	ts := hostRoutedServer(t, "facelabor.kafeido.app")
	defer ts.Close()

	health := probeHostname(dialToTestServer(t, ts), "spot-1", 443, "facelabor.kafeido.app", 2*time.Second)
	assert.True(t, health.Reachable)
	assert.Empty(t, health.Error)
	assert.Equal(t, "facelabor.kafeido.app", health.Hostname)
	assert.False(t, health.CheckedAt.IsZero())
}

// TestProbeHostname_UnreachableOn502 is the regression test for #1872's
// exact symptom: the sentinel successfully dials the tunnel and completes
// a TLS handshake (registration "looks" fine), but the primary's own
// Caddy has no route for this particular alias and answers 502.
func TestProbeHostname_UnreachableOn502(t *testing.T) {
	ts := hostRoutedServer(t, "facelabor.kafeido.app") // grpc.kafeido.app NOT configured
	defer ts.Close()

	health := probeHostname(dialToTestServer(t, ts), "spot-1", 443, "grpc.kafeido.app", 2*time.Second)
	assert.False(t, health.Reachable)
	assert.Contains(t, health.Error, "502")
}

func TestProbeHostname_DialFailureIsUnreachable(t *testing.T) {
	dial := func(string, int) (net.Conn, error) {
		return nil, assertErr("spot not registered")
	}
	health := probeHostname(dial, "spot-1", 443, "grpc.kafeido.app", 2*time.Second)
	assert.False(t, health.Reachable)
	assert.Contains(t, health.Error, "dial")
}

func TestProbeHostname_HandshakeFailureIsUnreachable(t *testing.T) {
	// A listener that accepts and immediately closes, so the TLS handshake
	// fails outright — simulating a dead/non-TLS port on the primary.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		conn, err := ln.Accept()
