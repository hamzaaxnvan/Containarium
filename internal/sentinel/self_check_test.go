package sentinel

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// wedgedListener accepts TCP connections and never reads/writes anything —
// simulating the exact production wedge: the listener is alive (TCP accepts
// succeed) but nothing downstream ever services the connection.
type wedgedListener struct {
	ln   net.Listener
	port int

	// conns retains every accepted connection until Close. Without a live
	// reference an accepted net.Conn is garbage: its runtime finalizer closes
	// the fd, the probe sees EOF — which selfCheckProxyPath rightly reads as
	// proof of life — and the "never responds" listener has responded.
	mu    sync.Mutex
	conns []net.Conn
}

func newWedgedListener(t *testing.T) *wedgedListener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	w := &wedgedListener{ln: ln, port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Deliberately never read or write — hold the connection open
			// until Close, same as a wedged dispatch pipeline.
			w.mu.Lock()
			w.conns = append(w.conns, conn)
			w.mu.Unlock()
		}
	}()
	return w
}

func (w *wedgedListener) Close() error {
	err := w.ln.Close()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range w.conns {
		_ = c.Close()
