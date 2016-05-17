package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BoxPhase is a high-level summary of a box's lifecycle, surfaced in
// Box.status.phase.
type BoxPhase string

const (
	// BoxPending — the Box has been accepted but its pod is not yet running.
	BoxPending BoxPhase = "Pending"
	// BoxRunning — the box pod is running and reachable.
	BoxRunning BoxPhase = "Running"
	// BoxSuspended — the box is parked (no pod) but its data is retained.
	BoxSuspended BoxPhase = "Suspended"
	// BoxTerminating — the Box is being deleted; its resources are being torn down.
	BoxTerminating BoxPhase = "Terminating"
	// BoxFailed — reconciliation could not realize the box.
	BoxFailed BoxPhase = "Failed"
)

// BoxResources is the requested CPU/memory/disk for a box. Values are
// substrate-native strings (e.g. "2", "4GB", "20GB"); empty fields take the
// daemon's defaults.
type BoxResources struct {
	// CPU request (e.g. "2").
	// +optional
	CPU string `json:"cpu,omitempty"`
	// Memory request (e.g. "4GB").
	// +optional
	Memory string `json:"memory,omitempty"`
	// Disk size for the box's workspace (e.g. "20GB").
	// +optional
	Disk string `json:"disk,omitempty"`
	// StorageClass for the box's workspace PVC. Empty = the daemon default.
	// +optional
	StorageClass string `json:"storageClass,omitempty"`
