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
