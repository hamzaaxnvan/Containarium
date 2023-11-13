package traffic

import (
	"context"
	"log"
	"net"
	"sync"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// containerLister is the subset of *incus.Client's surface Refresh needs,
// split out so the incremental-refresh logic (#1541) is unit-testable
// without a live Incus server — same "generic loop, production function
// injected by the caller" shape as fetchAllConcurrently in pkg/core/incus.
type containerLister interface {
	GetInstanceNames() ([]string, error)
	GetContainerWithNetwork(name string) (*incus.ContainerInfo, error)
}

// ContainerCache maps IP addresses to container names
type ContainerCache struct {
	incusClient containerLister
	network     *net.IPNet

	mu       sync.RWMutex
	ipToName map[string]string
	nameToIP map[string]string
	nameToID map[string]string // container name -> cloud_container_id label ("" on non-cloud boxes)
}

// NewContainerCache creates a new container cache
func NewContainerCache(incusClient *incus.Client, networkCIDR string) *ContainerCache {
	_, network, err := net.ParseCIDR(networkCIDR)
	if err != nil {
		log.Printf("Warning: failed to parse network CIDR %s: %v", networkCIDR, err)
	} else {
		log.Printf("Container cache network: %s (parsed from %s)", network.String(), networkCIDR)
	}
	return &ContainerCache{
		incusClient: incusClient,
		network:     network,
		ipToName:    make(map[string]string),
		nameToIP:    make(map[string]string),
		nameToID:    make(map[string]string),
	}
}

// LookupIP returns the container name for an IP address
func (c *ContainerCache) LookupIP(ip string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ipToName[ip]
}

// LookupName returns the IP for a container name
func (c *ContainerCache) LookupName(name string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nameToIP[name]
}

// LookupID returns the cloud_container_id label for a container name, or "" if
// the box is not a cloud-managed tenant (no label). Used to stamp container.id
// on egress fan-out metrics so they join to a tenant like the bytes plane.
func (c *ContainerCache) LookupID(name string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nameToID[name]
}

// IsContainerIP checks if an IP belongs to the container network
func (c *ContainerCache) IsContainerIP(ip string) bool {
	if c.network == nil {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	return c.network.Contains(parsed)
}

// GetAllContainers returns a copy of all container name to IP mappings
func (c *ContainerCache) GetAllContainers() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]string, len(c.nameToIP))
	for name, ip := range c.nameToIP {
		result[name] = ip
	}
	return result
}

// Refresh updates the cache from Incus incrementally (#1541): a full
// relist-and-refetch of every container on every cycle was found live to
