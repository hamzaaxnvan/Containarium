package sentinel

import (
	"crypto/tls"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// muxUnderTest runs a ConnMux and drains both of its listeners into
// channels so a test can observe which side a connection landed on.
type muxUnderTest struct {
	addr   string
	tunnel chan net.Conn
	https  chan net.Conn
}

func startMuxUnderTest(t *testing.T) *muxUnderTest {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	mux := NewConnMuxFromListener(ln)
	go mux.Run()
	t.Cleanup(func() { _ = mux.Close() })

	m := &muxUnderTest{
		addr:   ln.Addr().String(),
		tunnel: make(chan net.Conn, 8),
		https:  make(chan net.Conn, 8),
	}
	drain := func(l net.Listener, out chan net.Conn) {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			out <- c
		}
	}
	go drain(mux.TunnelListener(), m.tunnel)
	go drain(mux.HTTPSListener(), m.https)
	waitForListener(t, m.addr)
	return m
}

// send dials the mux, writes payload, and returns the listener the
// connection was routed to ("tunnel" or "https") together with the bytes the
// consumer reads back, which must equal the payload: the peek must replay
// every byte it looked at.
func (m *muxUnderTest) send(t *testing.T, payload []byte) (string, []byte) {
	t.Helper()
	conn, err := net.Dial("tcp", m.addr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Write(payload)
	require.NoError(t, err)
	// Half-close so a payload shorter than a TLS record (a bare record
	// header) is routed on EOF rather than on the peek deadline.
	require.NoError(t, conn.(*net.TCPConn).CloseWrite())

	readBack := func(c net.Conn) []byte {
		defer func() { _ = c.Close() }()
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		got := make([]byte, len(payload))
		_, err := io.ReadFull(c, got)
		require.NoError(t, err)
		return got
	}
	select {
	case c := <-m.tunnel:
		return "tunnel", readBack(c)
	case c := <-m.https:
		return "https", readBack(c)
	case <-time.After(3 * time.Second):
		t.Fatal("connection was not routed to either listener")
		return "", nil
	}
}

// TestConnMuxRoutesByALPN pins the routing table of the shared listener:
// a '{' goes to the tunnel path, a TLS ClientHello offering the tunnel ALPN
// goes to the tunnel path, and every other ClientHello (h2, http/1.1, no
// ALPN at all, the sentinel's own self-check probe) stays on the HTTPS path
// with its bytes intact.
func TestConnMuxRoutesByALPN(t *testing.T) {
	m := startMuxUnderTest(t)

	hello := func(cfg *tls.Config) []byte {
		cfg.InsecureSkipVerify = true // #nosec G402 -- only the ClientHello bytes are captured, no connection is made
		return captureClientHello(t, cfg)
	}
	alpnExt := func(protos ...string) []byte {
		var list []byte
