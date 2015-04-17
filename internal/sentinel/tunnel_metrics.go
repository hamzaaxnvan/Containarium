package sentinel

import (
	"bytes"
	"fmt"
)

// TunnelSessionStats is a point-in-time view of the tunnel sessions a
// sentinel holds, split by transport, plus the running count of cleartext
// sessions it has refused. Rendered on /metrics and /status so an operator
// can see peers that still need to move to the TLS transport.
type TunnelSessionStats struct {
	// TLS is the number of registered sessions carried over TLS.
	TLS int
	// CleartextRefused counts the cleartext sessions turned away since the
	// process started.
	CleartextRefused uint64
}

// renderTunnelMetrics builds the Prometheus exposition for the tunnel
// transport counters. Pure, so it is unit-testable without an HTTP round
// trip.
func renderTunnelMetrics(s TunnelSessionStats) string {
	var b bytes.Buffer
	fmt.Fprint(&b, "# HELP sentinel_tunnel_sessions_tls Registered tunnel sessions carried over TLS.\n")
