package waf

import (
	"bufio"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// echoServer accepts one connection and echoes lines back, so a test can prove
// bytes flowed client→proxy→upstream→proxy→client.
func echoServer(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c) // echo
			}(c)
		}
	}()
	return ln
}

// TestTransparentProxy_ForwardPath proves the steer→recover→forward→pipe path
// without a real transparent listener: OrigDst is overridden to point every
// accepted connection at the echo server (standing in for the original dst the
// kernel would set via IP_TRANSPARENT), and the proxy must forward bytes and
// pipe the echo back.
func TestTransparentProxy_ForwardPath(t *testing.T) {
	echo := echoServer(t)
	defer func() { _ = echo.Close() }()

	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("proxy listen: %v", err)
	}
	defer func() { _ = proxyLn.Close() }()

	var (
		mu       sync.Mutex
		forwards []string
	)
	p := &TransparentProxy{
		// Stand in for IP_TRANSPARENT's "LocalAddr == original dst".
		OrigDst: func(net.Conn) string { return echo.Addr().String() },
		OnForward: func(orig string) {
			mu.Lock()
			forwards = append(forwards, orig)
			mu.Unlock()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = p.Serve(ctx, proxyLn) }()

	conn, err := net.DialTimeout("tcp", proxyLn.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("hello-waf\n")); err != nil {
		t.Fatalf("write: %v", err)
