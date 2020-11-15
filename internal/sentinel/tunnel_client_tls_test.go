package sentinel

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestTunnelIdentity generates a throwaway sentinel tunnel identity.
func newTestTunnelIdentity(t *testing.T) *TunnelIdentity {
	t.Helper()
	id, err := GenerateTunnelIdentity()
	require.NoError(t, err)
	return id
}

// countingListener counts the application bytes its accepted connections
// deliver to the reader (for a TLS listener: decrypted bytes only).
type countingListener struct {
	net.Listener
	n *atomic.Int64
}

func (l countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return countingConn{Conn: c, n: l.n}, nil
}

type countingConn struct {
	net.Conn
	n *atomic.Int64
}

func (c countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// startEchoService runs a TCP echo server on 127.0.0.1 and returns its port.
func startEchoService(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestTunnelTLSEndToEnd: a client holding the sentinel's pin registers over
// TLS and a stream round-trips through the tunnel.
func TestTunnelTLSEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stubLoopbackAliases(t)
	const token = "tls-token"
	echoPort := startEchoService(t)
	id := newTestTunnelIdentity(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	registry := NewTunnelRegistry()
	server := NewTunnelServer("", policyAny(token), registry, 0)
	server.SetTunnelIdentity(id)
	connectCh := make(chan *TunnelSpot, 1)
	server.OnConnect = func(spot *TunnelSpot) {
		select {
		case connectCh <- spot:
		default:
		}
	}
	serveTunnelServer(t, ctx, server, ln)

	client := &TunnelClient{
		SentinelAddr: ln.Addr().String(),
		SentinelPins: []TunnelPin{id.Pin()},
		Token:        token,
		SpotID:       "tls-spot",
		Ports:        []int{echoPort},
	}
	go func() { _ = client.Run(ctx) }()

	select {
	case spot := <-connectCh:
		assert.Equal(t, "tls-spot", spot.ID)
		assert.Equal(t, TunnelTransportTLS, spot.Transport)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for TLS tunnel registration")
	}
	stats := registry.SessionStats()
	assert.Equal(t, 1, stats.TLS)

	stream, err := registry.DialTunnel("tls-spot", echoPort)
	require.NoError(t, err)
	defer func() { _ = stream.Close() }()
	_ = stream.SetDeadline(time.Now().Add(3 * time.Second))
	msg := []byte("hello over tls")
	_, err = stream.Write(msg)
	require.NoError(t, err)
	got := make([]byte, len(msg))
	_, err = io.ReadFull(stream, got)
	require.NoError(t, err)
	assert.Equal(t, msg, got)
}

// TestTunnelPinMismatchAbortsBeforeHandshake: the sentinel presents an
// identity the client does not pin. The client aborts inside the TLS
// handshake, so the server side receives zero application bytes and
// nothing registers.
//
// The first variant terminates TLS in front of the server so the bytes the
// server reads are exactly the decrypted application bytes, which must be
// zero. The second runs the server's own TLS listener path and checks the
// outcome the sentinel observes: no registration, no callback, no refusal
// counted.
func TestTunnelPinMismatchAbortsBeforeHandshake(t *testing.T) {
	serverID := newTestTunnelIdentity(t)
	pinnedID := newTestTunnelIdentity(t)

	t.Run("zero application bytes reach the server", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		var appBytes atomic.Int64
		registry := NewTunnelRegistry()
		server := NewTunnelServer("", policyAny("tok"), registry, 0)
		tlsLn := countingListener{Listener: tls.NewListener(ln, tunnelTLSServerConfig(serverID)), n: &appBytes}
		serveTunnelServer(t, ctx, server, tlsLn)

		client := &TunnelClient{
			SentinelAddr: ln.Addr().String(),
			SentinelPins: []TunnelPin{pinnedID.Pin()},
