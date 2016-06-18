package sentinel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsoleRouterPolicy_Validate(t *testing.T) {
	cases := []struct {
		name      string
		configure string
		presented string
		wantErr   bool
	}{
		{"matching", "secret", "secret", false},
		{"mismatched", "secret", "wrong", true},
		{"unconfigured policy", "", "secret", true},
		{"empty presented", "secret", "", true},
		{"both empty", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := NewConsoleRouterPolicy(c.configure)
			err := p.Validate(c.presented)
			if c.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestConsoleRouterIntegration exercises the full path a real console
// request takes: a caller dials the router, presents the router's own
// coarse-grained token plus a spot/port, the router calls
// TunnelRegistry.DialTunnel exactly like the SNI router and sshpiper
// already do for :443/:22, and the connection becomes a raw relay to
// whatever is listening on that advertised port — standing in here for
// the hypervisor-agent's own Agent.Serve (internal/hypervisor), which has
// its own handshake and auth one hop further in.
func TestConsoleRouterIntegration(t *testing.T) {
	withRecordedAliases(t)
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tunnelToken := "tunnel-integration-token"
	routerToken := "console-router-token"

	// Stand in for the hypervisor-agent's console listener: an echo server
	// on the "spot" side, reached by the same port-forward mechanism any
	// other tunnel-advertised service already uses.
	hvConsolePort := freePort(t)
	hvLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", hvConsolePort))
	require.NoError(t, err)
	defer func() { _ = hvLn.Close() }()
	go func() {
		for {
			conn, err := hvLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						if _, werr := c.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	// Sentinel side: ConnMux for the tunnel + TunnelServer + registry.
	muxPort := freePort(t)
	muxLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", muxPort))
	require.NoError(t, err)

	connMux := NewConnMuxFromListener(muxLn)
	go connMux.Run()
	defer func() { _ = connMux.Close() }()

	registry := NewTunnelRegistry()
	tunnelServer := NewTunnelServer("", policyAny(tunnelToken), registry, 0)
	id := newTestTunnelIdentity(t)
	tunnelServer.SetTunnelIdentity(id)

	connectCh := make(chan *TunnelSpot, 1)
