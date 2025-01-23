package sentinel

import (
	"bufio"
	"io"
	"log"
	"net"
	"slices"
	"sync"
	"time"
)

// ConnMux multiplexes a single TCP listener into tunnel and HTTPS connections
// based on the first bytes of each connection.
//
// A cleartext tunnel handshake starts with '{' (JSON). A TLS ClientHello
// (0x16) that offers the tunnel ALPN protocol (TunnelALPN) is a TLS tunnel
// session. Every other connection, including every other TLS ClientHello,
// is routed to HTTPS handling byte for byte.
//
// This allows the tunnel and HTTPS to share port 443, avoiding the need to
// open an extra port on the sentinel's firewall.
type ConnMux struct {
	listener net.Listener

	// Channel-based listeners that consumers Accept() from
	tunnelLn *chanListener
	httpsLn  *chanListener
}

// NewConnMux creates a multiplexer on the given address (e.g., ":443").
func NewConnMux(addr string) (*ConnMux, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return NewConnMuxFromListener(ln), nil
}

// NewConnMuxFromListener creates a multiplexer from an existing listener.
func NewConnMuxFromListener(ln net.Listener) *ConnMux {
	return &ConnMux{
		listener: ln,
		tunnelLn: newChanListener(ln.Addr()),
		httpsLn:  newChanListener(ln.Addr()),
	}
}

// TunnelListener returns a net.Listener that yields only tunnel connections:
// those whose first byte is '{' and TLS ClientHellos offering TunnelALPN.
func (cm *ConnMux) TunnelListener() net.Listener {
	return cm.tunnelLn
}

// HTTPSListener returns a net.Listener that yields all non-tunnel connections.
func (cm *ConnMux) HTTPSListener() net.Listener {
	return cm.httpsLn
}

// HTTPSChanListener returns the underlying chanListener for HTTPS.
// Used by Manager to create a dispatchListener for swapping consumers.
func (cm *ConnMux) HTTPSChanListener() *chanListener {
	return cm.httpsLn
}

// Run accepts connections and routes them. Blocks until the listener is closed.
func (cm *ConnMux) Run() {
	log.Printf("[conn-mux] multiplexing on %s (tunnel + HTTPS)", cm.listener.Addr())
	for {
		conn, err := cm.listener.Accept()
		if err != nil {
			// Listener closed
			_ = cm.tunnelLn.Close()
			_ = cm.httpsLn.Close()
			return
		}
		go cm.route(conn)
	}
}

// Close closes the underlying listener and both channel listeners.
func (cm *ConnMux) Close() error {
	_ = cm.tunnelLn.Close()
	_ = cm.httpsLn.Close()
	return cm.listener.Close()
}

// muxPeekTimeout bounds how long the mux waits for the bytes it routes on:
// the first byte, and for a TLS connection the rest of the ClientHello
// record. A peer that stalls inside its ClientHello is handed to the HTTPS
