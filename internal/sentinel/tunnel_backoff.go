package sentinel

import (
	cryptorand "crypto/rand"
	"math/big"
	"time"
)

const (
	reconnectInitialDelay = 1 * time.Second
	reconnectMaxDelay     = 60 * time.Second
	// reconnectStableAfter is how long a session must have lasted for its
	// disconnect to count as "the sentinel went away" and not "the connection
	// is flapping". A session at least this long resets the backoff.
	reconnectStableAfter = 30 * time.Second
)

// reconnectBackoff is the tunnel client's reconnect delay policy. Delays double
// across CONSECUTIVE failed or short-lived sessions, up to a cap, and start over
// after a session that stayed up. Without the reset a client that had been
// through enough disconnects over its life stayed at the cap forever, so every
// later sentinel restart cost it a full minute however healthy it had been.
type reconnectBackoff struct {
	cur time.Duration
}

func newReconnectBackoff() *reconnectBackoff {
	return &reconnectBackoff{cur: reconnectInitialDelay}
}

// next returns how long to wait before the next connection attempt, given how
// long the session that just ended lasted (0 for an attempt that never
// connected).
func (b *reconnectBackoff) next(sessionLasted time.Duration) time.Duration {
	if sessionLasted >= reconnectStableAfter {
		b.cur = reconnectInitialDelay
