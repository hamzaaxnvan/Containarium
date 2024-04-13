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
