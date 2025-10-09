package cluster

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Bootstrap renderers — pure functions producing the shell that runs
// inside a freshly created VM over `incus exec`. Rendered output is
// golden-tested (testdata/*.golden): a change to what executes inside
// a tenant's cluster VM must show up as a reviewable golden diff.
//
// Both scripts assume the pinned k3s binary was already pushed to
// K3sBinaryPath by the manager — bootstrap never downloads anything.

const (
	// K3sBinaryPath is where the manager pushes the pinned binary.
	K3sBinaryPath = "/usr/local/bin/k3s"
	// AgentTokenPath is where the manager pushes the join token (0600).
	AgentTokenPath = "/etc/containarium/k3s-agent-token"
	// KubeconfigPath is where k3s server writes the admin kubeconfig.
	KubeconfigPath = "/etc/rancher/k3s/k3s.yaml"
	// NodeTokenPath is where k3s server writes the agent join token.
	NodeTokenPath = "/var/lib/rancher/k3s/server/node-token"
)

// ServerBootstrap parameterizes the control-plane script.
type ServerBootstrap struct {
	// TLSSANs are extra subject-alt-names for the API server cert —
	// the external endpoint and the VM IP, so the rewritten
	// kubeconfig verifies.
	TLSSANs []string
	// Isolation selects the node runtime variant: the container path
	// prepends the boot-time /dev/kmsg shim into the unit (#1429);
	// the zero value renders the VM script byte-identically to
	// pre-#1429.
	Isolation Isolation
	// KubeletArgs are extra `k3s server` flags. k3s server runs an
	// embedded kubelet, so a container-class control plane needs the
	// same kubelet treatment a worker does (#1452). Empty on the VM
	// path, whose unit stays byte-identical to its golden.
	KubeletArgs []string
}

// RenderServerScript renders the control-plane first-boot script: a
// systemd unit for `k3s server`, tainted so tenant pods never schedule
// onto the platform-owned VM, traefik disabled (ingress is a later
// phase riding the platform's own routing).
func RenderServerScript(b ServerBootstrap) string {
	sans := append([]string(nil), b.TLSSANs...)
	sort.Strings(sans)
	var sanFlags strings.Builder
	for _, s := range sans {
		fmt.Fprintf(&sanFlags, " --tls-san %s", s)
	}
	return fmt.Sprintf(`#!/bin/sh
# containarium managed-cluster control-plane bootstrap (#1414).
# The k3s binary is pre-pushed; this script only wires and starts it.
set -eu

chmod 0755 %[1]s

cat > /etc/systemd/system/k3s.service <<'UNIT'
[Unit]
Description=containarium managed k3s server
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
%[5]sExecStart=%[1]s server --disable traefik --node-taint node-role.kubernetes.io/control-plane=:NoSchedule --write-kubeconfig-mode 0600%[2]s%[7]s
Restart=always
RestartSec=5
# A node legitimately takes longer than systemd's default 90s to signal
# readiness -- an agent waits for the server to finish coming up -- and a
# Type=notify unit that has not signalled by then is killed mid-start,
# restarted, and never joins (#1450). Upstream k3s's unit does the same.
TimeoutStartSec=0
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
UNIT
