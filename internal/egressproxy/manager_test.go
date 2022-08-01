package egressproxy

import (
	"io"
	"net"
	"testing"
	"time"
)

// TestManager_StartStop_AndForward: the manager starts a relay (port 0 ->
// discovers the bound addr), forwards an allowed source to the upstream, and
// Stop tears it down so the port is released.
func TestManager_StartStop_AndForward(t *testing.T) {
	// Echo upstream.
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream: %v", err)
	}
	defer func() { _ = up.Close() }()
	go func() {
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()

	m := NewManager()
	addr, err := m.Start("box-1", "127.0.0.1:0", up.Addr().String(), []string{"127.0.0.1/8"}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if addr == "" {
		t.Fatal("Start returned empty addr")
	}
	if m.Active() != 1 {
		t.Fatalf("Active = %d, want 1", m.Active())
	}
