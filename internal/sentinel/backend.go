package sentinel

import (
	"context"
	"sync"
)

// BackendType identifies the kind of backend.
type BackendType string

const (
	BackendGCP    BackendType = "gcp"
	BackendTunnel BackendType = "tunnel"
)

// Backend represents a single backend instance that the sentinel can forward to.
type Backend struct {
	ID           string
	Type         BackendType
	IP           string
	ExternalPort int           // externally reachable API port (for tunnel backends, e.g., 18001)
	Provider     CloudProvider // for diagnose/recover (GCP can restart VMs; tunnel cannot)
	Priority     int           // lower = higher priority for HTTP primary selection (GCP=0, tunnel=10)
	Pool         Pool          // optional pool tag (tunnel backends only); empty = unpooled

	// Per-backend health tracking
	Healthy        bool
	healthyCount   int
	unhealthyCount int

	// Sync loop cancellation
	syncCancel context.CancelFunc
}

// BackendPool manages multiple backends with thread-safe access.
type BackendPool struct {
	mu       sync.RWMutex
	backends map[string]*Backend
}

// NewBackendPool creates an empty backend pool.
func NewBackendPool() *BackendPool {
	return &BackendPool{
		backends: make(map[string]*Backend),
	}
}

// Add registers a backend. If a backend with the same ID exists, it is replaced.
func (bp *BackendPool) Add(b *Backend) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	// Cancel old sync loops if replacing
	if old, ok := bp.backends[b.ID]; ok && old.syncCancel != nil {
		old.syncCancel()
	}
	bp.backends[b.ID] = b
}

// Remove unregisters a backend and cancels its sync loops.
func (bp *BackendPool) Remove(id string) *Backend {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	b, ok := bp.backends[id]
	if !ok {
		return nil
	}
	if b.syncCancel != nil {
		b.syncCancel()
	}
