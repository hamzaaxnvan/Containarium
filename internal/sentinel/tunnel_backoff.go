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
