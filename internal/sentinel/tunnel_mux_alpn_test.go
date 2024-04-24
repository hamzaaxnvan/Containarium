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
