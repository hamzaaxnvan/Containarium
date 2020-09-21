package sentinel

import (
	"context"
	"fmt"
	"log"
)

// TunnelProvider implements CloudProvider using tunnel connection state.
// It reports the spot VM as "running" when the tunnel is connected and
// "stopped" when disconnected. The IP is the loopback alias assigned
// by the TunnelRegistry.
type TunnelProvider struct {
	registry *TunnelRegistry
	spotID   string // empty = use first connected spot
}

// NewTunnelProvider creates a provider backed by a tunnel registry.
