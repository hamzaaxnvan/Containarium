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
