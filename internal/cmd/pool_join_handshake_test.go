//go:build !containarium_client

package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// #1051: `pool join` printed "Joined pool" and exited 0 while the sentinel was
// rejecting the tunnel on every reconnect — 9,423 consecutive rejections over
// 11 days on a live BYOC host, with systemctl reporting the unit active
// throughout.

// Real log lines from internal/sentinel/tunnel_client.go.
const (
	lineConnected  = `Aug 09 12:00:01 host containarium-tunnel[1]: [tunnel-client] connected to sentinel edge.example:9443`
	lineRejected   = `Aug 09 12:00:01 host containarium-tunnel[1]: [tunnel-client] connection lost: handshake rejected: invalid token`
	lineRegistered = `Aug 09 12:00:02 host containarium-tunnel[1]: [tunnel-client] registered as "tunnel-abc", assigned IP 127.0.0.11`
	lineReconnect  = `Aug 09 12:00:03 host containarium-tunnel[1]: [tunnel-client] reconnecting to edge.example:9443 (backoff: 2s)...`
)

// THE trap. "connected to sentinel" is logged when the TCP connection is
// established, BEFORE the handshake is evaluated — so a host that is rejected
// every time logs it every time. Treating it as the success marker reproduces
// the exact bug this check exists to catch, while looking correct.
//
// The dangerous window is a poll landing between the connect and the
// rejection: the journal then contains ONLY the connect line. A marker of
// "connected to sentinel" reports that as joined; the real marker reports it
// as undecided and keeps waiting.
func TestClassifyTunnelJournal_ConnectedAloneIsNotSuccess(t *testing.T) {
	outcome, _ := classifyTunnelJournal(lineConnected)
	if outcome == tunnelHandshakeRegistered {
		t.Fatal("'connected to sentinel' was treated as a successful handshake. It is logged before " +
			"the handshake is evaluated, so a host about to be rejected looks joined — #1051 exactly")
	}
	if outcome != tunnelHandshakeUnknown {
