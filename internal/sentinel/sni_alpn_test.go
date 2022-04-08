package sentinel

import (
	"bufio"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureClientHello drives a stdlib TLS client into a pipe and returns the
// record-framed ClientHello bytes it emitted.
func captureClientHello(t *testing.T, cfg *tls.Config) []byte {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	go func() { _ = tls.Client(clientConn, cfg).Handshake() }()

	require.NoError(t, serverConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	br := bufio.NewReaderSize(serverConn, 16389)
	hdr, err := br.Peek(5)
	require.NoError(t, err)
	full, err := br.Peek(5 + (int(hdr[3])<<8 | int(hdr[4])))
	require.NoError(t, err)
	return append([]byte(nil), full...)
}

// buildClientHello hand-assembles a record-framed ClientHello carrying the
// given raw extensions block.
