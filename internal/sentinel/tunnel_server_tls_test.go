package sentinel

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTunnelServer serves ts on a fresh loopback TCP listener and returns
// the address peers dial.
// serveTunnelServer serves ts on ln and registers a cleanup that leaves no
// goroutine of the server behind: it stops Serve, closes the sessions that
// are still registered (which is what ends each monitorSession), and waits
// for every goroutine the server started to exit. Without the wait,
// monitorSession fires after the test returns and writes to the global
// logger and runs `ip addr del` during an unrelated later test, and the
// goroutines pile up across -count repetitions (#3009).
func serveTunnelServer(t *testing.T, ctx context.Context, ts *TunnelServer, ln net.Listener) {
	t.Helper()
	runTunnelServer(t, ctx, ts, func(c context.Context) error { return ts.Serve(c, ln) })
}

// runTunnelServer is serveTunnelServer for a server started by serve, such as
// TunnelServer.Run.
func runTunnelServer(t *testing.T, ctx context.Context, ts *TunnelServer, serve func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	served := make(chan struct{})
	go func() { defer close(served); _ = serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Errorf("tunnel server Serve did not return after its context was cancelled")
			return
		}
		// A connection still mid-handshake can register after the first
		// sweep, so keep closing sessions until every goroutine is gone.
		exited := make(chan struct{})
		go func() { ts.Wait(); close(exited) }()
		deadline := time.After(10 * time.Second)
		for {
			ts.registry.UnregisterAll()
			select {
			case <-exited:
				return
			case <-deadline:
				t.Errorf("tunnel server goroutines still running 10s after cleanup")
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	})
}

func startTunnelServer(t *testing.T, ctx context.Context, ts *TunnelServer) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serveTunnelServer(t, ctx, ts, ln)
	waitForListener(t, ln.Addr().String())
	return ln.Addr().String()
}

// TestCleartextPeerRefused: a peer that opens with the JSON handshake is
// answered with ok:false and closed before its handshake line is read. The peer only ever sends the opening brace,
// so a server that tried to read the line would hang instead of answering.
// Nothing registers, OnConnect never fires, and the refusal is counted.
func TestCleartextPeerRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	registry := NewTunnelRegistry()
	ts := NewTunnelServer("", policyAny("tok"), registry, 0)
	var connects atomic.Int32
	ts.OnConnect = func(*TunnelSpot) { connects.Add(1) }
	addr := startTunnelServer(t, ctx, ts)

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Write([]byte("{"))
	require.NoError(t, err)

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err := readHandshakeResponse(conn)
	require.NoError(t, err, "the refusal must arrive without the handshake line being read")
	assert.False(t, resp.OK)
	assert.Equal(t, "tls required; upgrade the tunnel client and set --sentinel-pin", resp.Error)

	// The server closes the connection after the refusal.
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	assert.Error(t, err, "connection must be closed after the refusal")

	assert.Zero(t, registry.Count())
	assert.Zero(t, connects.Load())
	stats := registry.SessionStats()
	assert.Equal(t, uint64(1), stats.CleartextRefused)
	assert.Zero(t, stats.TLS)
}

