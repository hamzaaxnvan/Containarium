package cluster

import (
	"fmt"
	"sort"
)

// The reconcile decision function — pure, exhaustively table-tested
// (the autosleep decide.go house pattern). The daemon-side loop builds
// Desired from the store record and Observed from the host, and
// executes whatever Decide returns; all policy lives here.
//
// Deliberately NOT here: scale-down. Removing surplus workers is the
// cluster-autoscaler's job (it drains first); the reconciler only
// creates up to each group's Min, restarts stopped VMs, and tears down
// deleting clusters. See the design's "Who decides what".

// DesiredGroup is one node group's desired shape.
type DesiredGroup struct {
	Name     string
	Min, Max int
	CPU      string
	Memory   string
	Disk     string
}

// Desired is a cluster's desired state, derived from the store record.
type Desired struct {
	Tenant  string
	Cluster string
	// Deleting means the record is in state DELETING: every VM goes.
	Deleting bool
	Groups   []DesiredGroup
}

// ObservedVM is one cluster VM as the host reports it.
type ObservedVM struct {
	Name    string
	Running bool
}

// Observed is the host's view of a cluster's VMs.
type Observed struct {
	CP *ObservedVM
	// Workers by group name. Names follow WorkerName; the observer
	// buckets them by the node_group label, not by parsing names.
	Workers map[string][]ObservedVM
}

// ActionKind enumerates what the reconciler can do in one pass.
type ActionKind int

const (
	ActionCreateCP ActionKind = iota
	ActionStartVM
	ActionCreateWorker
	ActionDeleteVM
)

func (k ActionKind) String() string {
	switch k {
	case ActionCreateCP:
		return "create-cp"
	case ActionStartVM:
		return "start-vm"
	case ActionCreateWorker:
		return "create-worker"
	case ActionDeleteVM:
		return "delete-vm"
	default:
		return fmt.Sprintf("actionkind(%d)", int(k))
	}
}

// Action is one step the loop executes.
type Action struct {
	Kind  ActionKind
	Group string // worker actions only
	// Name is the VM the action targets (for creates, the name to
	// create under).
	Name string
}

// Decide computes the actions that move Observed toward Desired.
// Invariants:
//   - a deleting cluster tears down workers first, control plane last;
//   - no worker is created before the control plane exists and runs
//     (agents need a server to join);
//   - stopped VMs are started, missing ones created — never both for
