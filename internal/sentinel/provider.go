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

// EventWatcher is an optional interface that CloudProviders can implement
// to proactively watch for VM lifecycle events (e.g., spot preemption).
// This enables the sentinel to react immediately rather than waiting for
// the TCP health check to detect the outage.
type EventWatcher interface {
	// WatchEvents sends VM lifecycle events to the channel.
	// Blocks until ctx is cancelled. The provider controls the polling interval.
	WatchEvents(ctx context.Context, events chan<- VMEvent) error
}

// VMEvent represents a lifecycle event on the backend VM.
type VMEvent struct {
	Type      VMEventType
	Timestamp time.Time
	Detail    string // human-readable description
}

// VMEventType classifies VM lifecycle events.
type VMEventType string

const (
	EventPreempted    VMEventType = "preempted"
	EventStopped      VMEventType = "stopped"
	EventStarted      VMEventType = "started"
	EventTerminated   VMEventType = "terminated"
	EventProvisioning VMEventType = "provisioning"
)

// InstanceStatus represents the state of a cloud VM instance.
type InstanceStatus string

const (
	StatusRunning      InstanceStatus = "running"
	StatusStopped      InstanceStatus = "stopped"
	StatusTerminated   InstanceStatus = "terminated"
	StatusProvisioning InstanceStatus = "provisioning"
	StatusUnknown      InstanceStatus = "unknown"
)
