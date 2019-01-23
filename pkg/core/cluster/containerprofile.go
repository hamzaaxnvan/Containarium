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
