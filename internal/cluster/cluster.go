// Package cluster holds the record types, validation, and persistence for
// managed Kubernetes clusters (#1413) — a platform-operated k3s control
// plane plus worker VMs in typed size classes.
//
// Design: docs/architecture/managed-k8s-clusters.md. This package owns the
// store-shaped state; VM provisioning and reconciliation live in
// pkg/core/cluster (#1414) and only communicate with this package through
// the Store interface.
package cluster

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var (
	// ErrNotFound is returned when a cluster (or node) does not exist.
	ErrNotFound = errors.New("cluster not found")
	// ErrAlreadyExists is returned when creating a cluster whose
	// (owner, name) is already taken.
	ErrAlreadyExists = errors.New("cluster already exists")
)

// State is a cluster's lifecycle state. Mirrors pb.ClusterState; the
// string form is what the store persists.
type State string

const (
	StateProvisioning State = "provisioning"
	StateReady        State = "ready"
	StateDegraded     State = "degraded"
	StateDeleting     State = "deleting"
	StateError        State = "error"
)

// EventKind categorizes scale-history entries. Mirrors pb.ScaleEventKind.
type EventKind string

const (
	EventScaleUp      EventKind = "scale_up"
	EventScaleDown    EventKind = "scale_down"
	EventRefused      EventKind = "refused"
	EventNodeReplaced EventKind = "node_replaced"
)

// Node roles as persisted on node rows.
const (
	RoleControlPlane = "control-plane"
	RoleWorker       = "worker"
)

// Isolation is a cluster's node isolation class — the boundary that
// contains a tenant kernel exploit. Mirrors pb.NodeIsolation; the
// string form is what the store persists.
//
// Design: docs/architecture/cluster-container-node-pools.md.
type Isolation string

const (
	// IsolationVM gives every node its own kernel (Incus VM). The
	// default, and the only class safe on a shared multi-tenant host.
	IsolationVM Isolation = "vm"
	// IsolationContainer runs nodes as Incus system containers sharing
	// the host kernel — operator-gated, single-trust-domain hosts only.
	IsolationContainer Isolation = "container"
)

// OrDefault resolves an unset class to the strong one. The weaker
// class is only ever reached by an explicit request; omitting the
// field can never downgrade a boundary.
func (i Isolation) OrDefault() Isolation {
	if i == "" {
		return IsolationVM
	}
	return i
}

// NodeState is a node's coarse lifecycle state. Mirrors
// pb.ClusterNodeState; typed so the reconciler cannot persist a value
// the API cannot express.
type NodeState string

const (
	NodeStateProvisioning NodeState = "provisioning"
	NodeStateReady        NodeState = "ready"
	NodeStateDraining     NodeState = "draining"
)

// Size is a node VM size in the house resource-limit format
// (cpu "4", memory "8GB", disk "80GB"). GPU fields are deliberately
// absent: GPU node pools are a later phase and requests carrying them
// are rejected at the API boundary.
type Size struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
	Disk   string `json:"disk"`
}

// NodeGroup is a typed worker size class with autoscale bounds.
type NodeGroup struct {
	Name     string `json:"name"`
	Size     Size   `json:"size"`
	MinNodes int32  `json:"min_nodes"`
	MaxNodes int32  `json:"max_nodes"`
	// TargetNodes is the autoscaler-owned desired size (#1415): the
	// CA provider raises/lowers it and the reconciler converges to
	// it. Zero/unset means "no autoscaler decision yet" — the
	// effective target is then MinNodes. Never persisted below
	// MinNodes or above MaxNodes.
	TargetNodes int32 `json:"target_nodes,omitempty"`
}

// EffectiveTarget is the node count the reconciler converges the group
// to: the autoscaler's target clamped into [MinNodes, MaxNodes].
func (g NodeGroup) EffectiveTarget() int32 {
	t := g.TargetNodes
	if t < g.MinNodes {
		t = g.MinNodes
	}
	if t > g.MaxNodes {
		t = g.MaxNodes
	}
	return t
}

// Cluster is the persisted record of a managed cluster.
type Cluster struct {
	ID          string
	Owner       string
	Name        string
	State       State
	StateReason string
	K3sVersion  string
	APIEndpoint string
	NodeGroups  []NodeGroup
	// NodeIsolation is the cluster's isolation class, fixed at create
