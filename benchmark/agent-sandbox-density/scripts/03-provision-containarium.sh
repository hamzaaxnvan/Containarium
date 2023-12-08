#!/bin/bash
#
# Provision the experiment-group VM: the same shared base cluster as the
# control group, INCLUDING gVisor (k8s-common.sh — gVisor is identical on
# both sides, see README.md "What's actually under test"), plus the
# Containarium daemon (Helm chart) configured to schedule every box pod
# under the same runsc RuntimeClass — pod -> gVisor -> containarium.
#
# Gateway (SSH) routing: sshpiper itself is disabled (gateway.enabled=
# false) — this benchmark only needs to know whether a box reached
# RUNNING, not to SSH into it. gateway.namespace is left at its chart
# default ("agent-gateway") rather than cleared, though — found live that
# charts/containarium-k8s/templates/namespace.yaml unconditionally creates
# a Namespace resource from gateway.namespace with no enabled-guard, so an
# empty value breaks `helm install` outright ("resource name may not be
# empty"). The values.yaml comment's documented way to fully disable
# gateway routing (clear `namespace`, leave the two upstream-key values
# empty) doesn't actually work end-to-end as shipped — this works around
# it by keeping the namespace non-empty and supplying a throwaway upstream
# keypair instead, the same shape KIND-QUICKSTART.md documents for "Pipe
# objects get programmed, nothing is actually watching them yet". If you
# DO want to reach the boxes afterward, note that `kubectl exec`/
# `port-forward` straight to a gVisor-scheduled box pod does not work
# (kubernetes-sigs/agent-sandbox#158, this repo's #1489) — see
# docs/K8S-AGENT-BOX-RUNTIME-DESIGN.md "Hard isolation via RuntimeClass"
# and stand up a real sshpiper per docs/KIND-QUICKSTART.md if needed.
#
# Usage:
#   scripts/03-provision-containarium.sh --name <vm-name>
#
# Assumes the guest is Ubuntu Server 24.04 with passwordless sudo for
# REMOTE_SSH_USER, same as 01-provision-k8s-control.sh.

set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=lib.sh
source ./lib.sh
# shellcheck source=k8s-common.sh
source ./k8s-common.sh

VM_NAME=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--name)
