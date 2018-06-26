package cluster

// Container node profile and capability probe (#1429): THE one place
// holding the "k3s node inside an Incus system container" knowledge.
// Design: docs/architecture/cluster-container-node-pools.md
// ("The container node profile").
//
// The profile is deliberately three facts and a hard line:
//
//   - security.nesting=true — containerd runs pods inside the node.
//   - cgroup v2 delegation — Incus's default for system containers;
//     nothing to set here, the host-side requirement (a unified
//     cgroup v2 hierarchy) is asserted by ContainerNodeCapable.
//   - a boot-time /dev/kmsg shim rendered into the bootstrap unit
//     (KmsgShimCommand below).
//   - NO security.privileged — if a future k8s feature turns out to
//     need it, that is a new design conversation, not a config tweak.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContainerNodeConfig returns the Incus instance config applied to a
// k3s node container by CreateNode on the container path. Golden-
// pinned (testdata/container-node-config.golden): what a tenant's node
// container is granted changes only as a reviewable golden diff.
func ContainerNodeConfig() map[string]string {
	return map[string]string{
		// containerd runs pods inside the node container.
		"security.nesting": "true",
	}
}

// KmsgShimCommand is the boot-time /dev/kmsg shim: k3s's kubelet wants
// /dev/kmsg, which an unprivileged container does not get — the
// k3d-proven workaround links it to the console. It renders into the
// k3s systemd unit as an ExecStartPre so it re-applies on every boot
// (/dev is a fresh tmpfs each start), rather than being mounted from
// the host, whose own /dev/kmsg may not exist (nested case).
const KmsgShimCommand = `/bin/sh -c '[ -e /dev/kmsg ] || ln -s /dev/console /dev/kmsg'`

// kmsgShimUnitLine renders the shim into a unit's [Service] section:
// one ExecStartPre line on the container path, nothing on the VM path
// — VM units stay byte-identical to their pre-#1429 goldens.
func kmsgShimUnitLine(iso Isolation) string {
	if iso != IsolationContainer {
		return ""
	}
	return "ExecStartPre=" + KmsgShimCommand + "\n"
}

// --- capability probe -------------------------------------------------

// ErrContainerNodesUnsupported heads every ContainerNodeCapable
// refusal — the container-class counterpart of ErrVMsUnsupported.
var ErrContainerNodesUnsupported = errors.New("this backend cannot run container nodes")

// ProbeState is one precondition's observed state.
type ProbeState int

const (
	// ProbeOK: the precondition was verified present.
	ProbeOK ProbeState = iota
	// ProbeMissing: the precondition was verified absent.
	ProbeMissing
	// ProbeUnknown: the precondition could not be observed. Never
	// treated as OK — an unverified boundary requirement refuses,
	// with the refusal saying what could not be checked and why.
	ProbeUnknown
)

// Probe is one precondition's observation: its state plus, for
// anything other than OK, the incusenv "which step, which error"
// detail an operator needs to act on it.
type Probe struct {
	State  ProbeState
	Detail string
}
