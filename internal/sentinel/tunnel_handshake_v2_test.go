package sentinel

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingListener captures every byte the server reads from each
// accepted connection: the raw wire bytes as the peer sent them. Under a
// TLS session that is ciphertext; under the cleartext path it is the JSON
// handshake itself.
type recordingListener struct {
	net.Listener
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *recordingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &recordingConn{Conn: c, l: l}, nil
}

func (l *recordingListener) captured() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.buf.Bytes()...)
}

type recordingConn struct {
	net.Conn
	l *recordingListener
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.l.mu.Lock()
	c.l.buf.Write(p[:n])
	c.l.mu.Unlock()
	return n, err
}

// serveV2Tunnel starts a TunnelServer holding id on a recording listener
// and returns the server, the listener (for its capture) and its address.
func serveV2Tunnel(t *testing.T, ctx context.Context, policy *TokenPolicy, registry *TunnelRegistry, id *TunnelIdentity) (*TunnelServer, *recordingListener, string) {
	t.Helper()
	ts := NewTunnelServer("", policy, registry, 0)
	ts.SetTunnelIdentity(id)
	ln, addr := serveRecorded(t, ctx, ts)
	return ts, ln, addr
}

// serveRecorded serves an already configured ts on a recording loopback
// listener and returns the listener and the address peers dial.
func serveRecorded(t *testing.T, ctx context.Context, ts *TunnelServer) (*recordingListener, string) {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ln := &recordingListener{Listener: raw}
	serveTunnelServer(t, ctx, ts, ln)
	waitForListener(t, raw.Addr().String())
	return ln, raw.Addr().String()
}

// dialTunnelTLS opens a TLS tunnel session to addr the way TunnelClient
// does (TLS 1.3, tunnel ALPN, pinned identity) and returns the connection
// after the handshake, so a test can write its own application bytes.
func dialTunnelTLS(t *testing.T, addr string, id *TunnelIdentity) *tls.Conn {
	t.Helper()
	tc := &TunnelClient{SentinelAddr: addr, SentinelPins: []TunnelPin{id.Pin()}}
	cfg, err := tc.tlsConfig()
	require.NoError(t, err)
	raw, err := net.DialTimeout("tcp", addr, 3*time.Second)
	require.NoError(t, err)
	conn := tls.Client(raw, cfg)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	require.NoError(t, conn.Handshake())
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestTunnelTokenNeverOnTheWire: the raw bytes of a successful TLS tunnel
// session never contain the token. The control variant runs the same
// capture under the legacy cleartext handshake, where the token IS in the
// capture, so the assertion is known to be able to fail.
func TestTunnelTokenNeverOnTheWire(t *testing.T) {
	stubLoopbackAliases(t)
	const token = "wire-token-0123456789abcdef"

	t.Run("tls session", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		id := newTestTunnelIdentity(t)
		registry := NewTunnelRegistry()
		ts, ln, addr := serveV2Tunnel(t, ctx, policyAny(token), registry, id)
		connectCh := make(chan *TunnelSpot, 1)
		ts.OnConnect = func(spot *TunnelSpot) {
			select {
			case connectCh <- spot:
			default:
			}
		}

		client := &TunnelClient{
			SentinelAddr: addr,
			SentinelPins: []TunnelPin{id.Pin()},
			Token:        token,
			SpotID:       "v2-spot",
			Ports:        []int{22},
		}
		go func() { _ = client.connectAndServe(ctx) }()

		select {
		case spot := <-connectCh:
