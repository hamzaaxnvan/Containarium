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

