package sentinel

import (
	"crypto/tls"
	"io"
	"net"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acceptCountListener counts accepted connections so a test can see how many
// hops one client connection fans out into.
type acceptCountListener struct {
	net.Listener
	accepted atomic.Int64
}

func (c *acceptCountListener) Accept() (net.Conn, error) {
	conn, err := c.Listener.Accept()
	if err == nil {
		c.accepted.Add(1)
	}
	return conn, err
}

// TestFallbackToOwnListenerIsRefused guards the production failure where the
// passthrough fallback was a tunnel backend's loopback alias (127.0.0.N:443).
// The ConnMux listens on the wildcard :443, so that dial connects back to the
// sentinel; the connection is routed again, falls back again, and a SINGLE
// client connection fans out into an unbounded chain (about 100 KB per hop;
// tens of thousands of hops within seconds). With the 384M cgroup cap that is a
// sentinel OOM kill a fraction of a second after the first unmatched
// connection, every time a failover lands on a tunnel backend.
//
// Here 127.0.0.1 plays the alias and the mux's own port plays :443. The handler
// must refuse to dial it, so the one client connection stays one connection.
func TestFallbackToOwnListenerIsRefused(t *testing.T) {
	// Goroutines other tests started and have not finished stopping are not
	// this test's to account for, and a fixed ceiling grows with every -count
	// repetition. Measure what THIS test adds on top of where it began.
	baseline := settledGoroutines()

	rawLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ln := &acceptCountListener{Listener: rawLn}
	mux := NewConnMuxFromListener(ln)
	go mux.Run()
	defer func() { _ = mux.Close() }()

	muxPort := rawLn.Addr().(*net.TCPAddr).Port
	dl := newDispatchListener(mux.HTTPSChanListener())
	m := &Manager{
		primaries:        NewPrimaryRegistry(),
		proxyIdleTimeout: 120 * time.Second, // production sniProxyIdleTimeout
		config:           Config{ProxyProtocol: true, HTTPSPort: muxPort},
	}
	// The fallback is the mux's own address, exactly like fallback=<alias>:443.
