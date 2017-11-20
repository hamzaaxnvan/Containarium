package sentinel

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTunnelIntegration is a full integration test that simulates:
//   - A sentinel running with --provider=tunnel (ConnMux on a single port)
//   - A firewalled spot VM connecting via reverse tunnel
//   - End-user HTTPS traffic flowing through the tunnel to the spot's service
//   - Health checks succeeding through the tunnel
//
// Everything runs on localhost. No iptables, no loopback aliases, no real VMs.
// The test verifies the core tunnel flow: mux → tunnel handshake → yamux → spot service.
func TestTunnelIntegration(t *testing.T) {
	withRecordedAliases(t)

	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	token := "integration-test-token"

	// ---------------------------------------------------------------
	// 1. Start mock services on the "spot VM" side
	// ---------------------------------------------------------------

	// Mock health/API daemon on port 8080-equivalent
	spotDaemonPort := freePort(t)
	spotDaemonLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", spotDaemonPort))
	require.NoError(t, err)
	defer func() { _ = spotDaemonLn.Close() }()

	spotDaemonSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/health":
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"status":"healthy"}`))
			case "/authorized-keys":
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"keys":[{"username":"testuser","authorized_keys":"ssh-rsa AAAA..."}]}`))
			default:
				w.WriteHeader(404)
			}
		}),
	}
	go func() { _ = spotDaemonSrv.Serve(spotDaemonLn) }()
	defer func() { _ = spotDaemonSrv.Close() }()

	// Mock HTTPS service on the spot (e.g., Caddy)
	spotHTTPSPort := freePort(t)
	spotHTTPSLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", spotHTTPSPort))
	require.NoError(t, err)
	defer func() { _ = spotHTTPSLn.Close() }()

	spotHTTPSSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("hello from spot HTTPS"))
		}),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{mustSelfSignedCert(t)},
		},
	}
	go func() { _ = spotHTTPSSrv.ServeTLS(spotHTTPSLn, "", "") }()
	defer func() { _ = spotHTTPSSrv.Close() }()

	// ---------------------------------------------------------------
	// 2. Start sentinel-side: ConnMux + TunnelServer + Manager
	// ---------------------------------------------------------------

	muxPort := freePort(t)
	muxLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", muxPort))
	require.NoError(t, err)

	connMux := NewConnMuxFromListener(muxLn)
	go connMux.Run()
	defer func() { _ = connMux.Close() }()

	registry := NewTunnelRegistry()
	tunnelServer := NewTunnelServer("", policyAny(token), registry, 0)
	id := newTestTunnelIdentity(t)
	tunnelServer.SetTunnelIdentity(id)

	// These callbacks run on the tunnel server's own goroutines, which
	// OUTLIVE this function: a session closing after the test returns still
	// invokes OnDisconnect. So they must not touch `t` at all.
	//
	// They used to log through t.Logf, and the data race that produced was
	// real — the read of the test's state raced tRunner's write marking the
	// test complete (#1374). Calling t.Log after a test returns is
	// documented misuse; here it also fired only sometimes, depending on
	// when the session happened to close, so it presented as a flake in
	// unrelated PRs.
	//
	// The sends are non-blocking for the same reason. A buffered-1 channel
	// accepts the first event; a second would block the server's goroutine
	// forever, and nothing reads disconnectCh at all.
	connectCh := make(chan *TunnelSpot, 1)
	disconnectCh := make(chan string, 1)
	tunnelServer.OnConnect = func(spot *TunnelSpot) {
		select {
		case connectCh <- spot:
		default:
		}
	}
	tunnelServer.OnDisconnect = func(spot *TunnelSpot) {
		select {
		case disconnectCh <- spot.ID:
		default:
		}
	}

	serveTunnelServer(t, ctx, tunnelServer, connMux.TunnelListener())

	time.Sleep(100 * time.Millisecond)

	// ---------------------------------------------------------------
	// 3. Start tunnel client on the "spot VM"
	// ---------------------------------------------------------------

	// The client's TLS session reaches the tunnel server through the mux,
	// which routes it by ALPN.
	client := &TunnelClient{
		SentinelAddr: fmt.Sprintf("127.0.0.1:%d", muxPort),
		SentinelPins: []TunnelPin{id.Pin()},
		Token:        token,
		SpotID:       "integration-spot",
		Ports:        []int{spotDaemonPort, spotHTTPSPort},
	}
	go func() { _ = client.Run(ctx) }()

	// Wait for connection
	var spot *TunnelSpot
	select {
	case spot = <-connectCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for tunnel connection")
	}

	assert.Equal(t, "integration-spot", spot.ID)
	t.Logf("[integration] tunnel established, spot at %s", spot.LocalIP)

	// ---------------------------------------------------------------
	// 4. Verify: health check through tunnel
	// ---------------------------------------------------------------

	time.Sleep(200 * time.Millisecond)

	// The health check dials spotIP:daemonPort through the tunnel proxy.
	// On Linux this would be 127.0.0.2:daemonPort. On macOS it won't work
	// (loopback alias doesn't exist), so we test via yamux stream directly.
	t.Run("health_check_via_yamux", func(t *testing.T) {
		// Open a yamux stream to the spot's daemon port
		stream, err := spot.Session.Open()
		require.NoError(t, err)
		defer func() { _ = stream.Close() }()

		// Send port header
		portBytes := []byte{byte((spotDaemonPort >> 8) & 0xff), byte(spotDaemonPort & 0xff)}
		_, err = stream.Write(portBytes)
		require.NoError(t, err)

		// Send HTTP request
		_, err = stream.Write([]byte("GET /health HTTP/1.0\r\nHost: localhost\r\n\r\n"))
		require.NoError(t, err)

