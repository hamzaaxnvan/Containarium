package reqrate

import (
	"sort"
	"strings"
)

// Container identifies a tenant container for request-rate attribution. The
// collector builds these from its existing container list — Name + primary IP
// the edge dials, plus the cloud_container_id label (empty on non-cloud boxes).
type Container struct {
	Name        string // incus container name
	IP          string // primary IPv4 the edge reverse-proxies to
	ContainerID string // cloud_container_id label; "" on standalone boxes
}

// Route maps an edge hostname to the upstream IP it proxies to — the subset of
// ProxyManager.ListRoutes the resolver needs.
type Route struct {
	Host       string
	UpstreamIP string
}

// Resolver maps a request host to the container serving it, by joining routes
// (host→upstream IP) with the container list (IP→container). Built once per
// collection tick from data the collector already has; cheap and immutable
// after construction.
type Resolver struct {
	byHost map[string]Container
}

// NewResolver joins routes and containers into a host→container map. A route
// whose upstream IP matches no container is dropped (the container may have
// gone away); a container with no IP can't be reverse-proxied so it's skipped.
func NewResolver(routes []Route, containers []Container) *Resolver {
	byIP := make(map[string]Container, len(containers))
	for _, c := range containers {
		if c.IP != "" {
			byIP[c.IP] = c
		}
	}
	byHost := make(map[string]Container, len(routes))
	for _, r := range routes {
		if c, ok := byIP[r.UpstreamIP]; ok {
			byHost[strings.ToLower(r.Host)] = c
		}
	}
	return &Resolver{byHost: byHost}
}

