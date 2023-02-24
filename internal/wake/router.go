package wake

import (
	"context"
	"fmt"
	"log"

	"github.com/footprintai/containarium/internal/app"
)

// ProxyManager is the subset of *app.ProxyManager that Router needs.
// Defined as an interface so unit tests can fake the Caddy mutation
// without standing up the real admin API.
type ProxyManager interface {
	UpdateRoute(subdomain, containerIP string, port int) error
	UpdateGRPCRoute(subdomain, containerIP string, port int) error
}

// Router applies the route swap between "direct" (Caddy → container)
// and "wake" (Caddy → daemon wake handler) modes. The tracker is the
// authoritative state; we update it before we touch Caddy so a racing
// RouteSyncJob tick either sees the new state and pushes the same
// upstream, or sees the old state and we overwrite its push moments
// later.
