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
