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
// If spotID is empty, it uses the first connected spot.
func NewTunnelProvider(registry *TunnelRegistry, spotID string) *TunnelProvider {
	return &TunnelProvider{
		registry: registry,
		spotID:   spotID,
	}
}

func (tp *TunnelProvider) GetInstanceIP(ctx context.Context) (string, error) {
	spot := tp.getSpot()
	if spot == nil {
		return "", fmt.Errorf("no tunnel-connected spot instance")
	}
	return spot.LocalIP, nil
}

func (tp *TunnelProvider) GetInstanceStatus(ctx context.Context) (InstanceStatus, error) {
	spot := tp.getSpot()
	if spot == nil {
		return StatusStopped, nil
	}
	if spot.Session.IsClosed() {
		return StatusStopped, nil
	}
	return StatusRunning, nil
}

func (tp *TunnelProvider) StartInstance(ctx context.Context) error {
	log.Printf("[tunnel-provider] cannot remotely start a firewalled spot instance — it must reconnect on its own")
	return fmt.Errorf("remote spot instances must reconnect on their own")
}

func (tp *TunnelProvider) getSpot() *TunnelSpot {
	if tp.spotID != "" {
		return tp.registry.Get(tp.spotID)
	}
	return tp.registry.GetFirst()
}
