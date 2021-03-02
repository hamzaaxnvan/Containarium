package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/footprintai/containarium/pkg/version"
)

// healthStatus is the /health "status" value.
type healthStatus string

const (
	healthStatusHealthy healthStatus = "healthy"
	// healthStatusDegraded: the daemon is up and answering, but a dependency
	// is not ready yet (#3130). Served with HTTP 200 on purpose — the
	// backend is live, and a liveness probe or tunnel must not treat it as
	// down.
	healthStatusDegraded healthStatus = "degraded"
)

// startupRetryAfterSeconds is the Retry-After hint on the 503s served while
// the daemon is starting.
const startupRetryAfterSeconds = "5"

// StartupListener binds the daemon's HTTP port before the startup waits
// (core containers, Postgres) so the backend is reachable while they run
// (#3130). Until Serve hands it the real gateway handler it answers /health
// with status "degraded" and the current reason, and every other path with
// 503 Service Unavailable — nothing that needs a not-yet-ready dependency is
// served half-initialised.
//
// The listener is never closed in between: Serve swaps the handler on the
// same http.Server, so there is no window where connections are refused.
type StartupListener struct {
	lis net.Listener
	srv *http.Server

	mu      sync.RWMutex
	reason  string
	handler http.Handler // nil until Serve

	done    chan struct{}
	serveMu sync.Mutex
	err     error
}

// ListenStartup binds addr and starts serving the startup responses with
// reason. reason must be a fixed operator-facing phrase, never an error
// string: /health is unauthenticated.
func ListenStartup(addr, reason string) (*StartupListener, error) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("bind %s for the startup health endpoint: %w", addr, err)
	}
	sl := &StartupListener{lis: lis, reason: reason, done: make(chan struct{})}
	sl.srv = &http.Server{Handler: sl, ReadHeaderTimeout: 30 * time.Second}
	go func() {
		err := sl.srv.Serve(lis)
		sl.serveMu.Lock()
		sl.err = err
		sl.serveMu.Unlock()
		close(sl.done)
	}()
	return sl, nil
}
