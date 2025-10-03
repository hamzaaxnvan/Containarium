package egressproxy

import (
	"context"
	"sync"
)

// Manager tracks per-key egress relays (keyed by box/container name) so the
// daemon can start one when an operator requests "egress via client" and tear
// it down on stop / disconnect. Lifetimes are process-local and ephemeral —
// relays are not persisted across a daemon restart (the operator's ssh -R dies
// with the daemon anyway, so a stale relay would be useless).
type Manager struct {
	mu     sync.Mutex
	relays map[string]context.CancelFunc
}

// NewManager returns an empty Manager.
func NewManager() *Manager {
	return &Manager{relays: make(map[string]context.CancelFunc)}
}

// Start (re)starts the relay for key. Any existing relay for the same key is
// torn down first (idempotent re-invoke). It binds synchronously so a bind
