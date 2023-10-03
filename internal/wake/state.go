// Package wake implements the "wake-on-HTTP" piece of the auto-sleep
// feature: when an auto-sleeping container's HTTP route is hit, Caddy
// proxies the request to the daemon's wake handler, the handler starts
// the container and waits until it's ready, then proxies the request
// through. Caddy's route is also flipped back to point at the container
// directly so subsequent requests bypass the daemon.
//
// The package contains three small pieces:
//
//   - WakeStateTracker: in-memory map of "which container names are
//     currently in wake mode". Read by RouteSyncJob before pushing a
//     route's upstream to Caddy — if the container is in wake mode,
//     RouteSync pushes the wake-handler address instead of the route's
//     direct target IP/port. Without this coordination the next
//     RouteSync tick (every ~5–30s) would silently revert the swap.
//
//   - Router: applies a route swap. On sleep, points Caddy at the
//     daemon's wake address and marks the tracker. On start, points
//     Caddy back at the container and clears the tracker.
//
//   - WakeProxy: HTTP handler at /wake/. Resolves the incoming Host
//     header to a container, wakes it (coalescing concurrent requests),
//     then reverse-proxies through to the now-running container. Also
//     fires-and-forgets a SwapToDirect so subsequent requests skip the
//     daemon entirely.
package wake

import (
	"sync"
)

