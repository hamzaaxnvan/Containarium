package sentinel

import (
	"context"
	"time"
)

// CloudProvider abstracts cloud-specific VM operations.
// TCP health checking is cloud-agnostic (in healthcheck.go).
// The provider is ONLY called when TCP check fails — to diagnose why and take action.
type CloudProvider interface {
	// GetInstanceStatus returns the VM's status (called only when TCP health check fails)
	GetInstanceStatus(ctx context.Context) (InstanceStatus, error)
	// GetInstanceIP returns the VM's internal/private IP
	GetInstanceIP(ctx context.Context) (string, error)
	// StartInstance attempts to start/restart the VM
	StartInstance(ctx context.Context) error
}

