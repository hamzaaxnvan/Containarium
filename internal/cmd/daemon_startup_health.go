//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/footprintai/containarium/internal/gateway"
)

// Reasons /health reports while the daemon is starting (#3130). Fixed
// phrases, never error text: /health is unauthenticated.
const (
	startupReasonStarting       = "starting"
	startupReasonCoreContainers = "waiting for core containers"
	startupReasonWaitPostgres   = "waiting for postgres"
	startupReasonPostgres       = "postgres unavailable"
	startupReasonInitialising   = "initialising daemon"
)

// startupHealth is the HTTP port bound before the startup waits, answering
// /health "degraded" until the gateway takes it over. A zero value (nothing
// bound) is valid: every method is a no-op.
type startupHealth struct {
	listener *gateway.StartupListener
}

// bindStartupHealth binds host:port for the startup health endpoint when REST
// is enabled. A bind failure is logged and startup carries on without it —
// the gateway then binds the port itself and reports the error as it always
// did.
func bindStartupHealth(enableREST bool, host string, port int) startupHealth {
	if !enableREST {
		return startupHealth{}
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	sl, err := gateway.ListenStartup(addr, startupReasonStarting)
	if err != nil {
		log.Printf("Warning: could not bind the HTTP port early for /health (%v); it opens once startup finishes", err)
		return startupHealth{}
	}
	log.Printf("HTTP port %s bound early: /health reports degraded until startup finishes", sl.Addr())
	return startupHealth{listener: sl}
}

// SetReason updates the degraded reason on /health.
func (s startupHealth) SetReason(reason string) {
	if s.listener != nil {
		s.listener.SetReason(reason)
	}
}

// Close releases the port.
func (s startupHealth) Close() {
	if s.listener != nil {
		_ = s.listener.Close()
	}
}

// releaseUnlessServing returns the listener for the gateway to take over, or
// closes it and returns nil when the final config (which the persisted
// daemon config may have changed) no longer serves REST on the bound port.
func (s startupHealth) releaseUnlessServing(enableREST bool, port int) *gateway.StartupListener {
	if s.listener == nil {
		return nil
	}
	bound := 0
	if tcp, ok := s.listener.Addr().(*net.TCPAddr); ok {
		bound = tcp.Port
	}
	if enableREST && bound == port {
		return s.listener
	}
