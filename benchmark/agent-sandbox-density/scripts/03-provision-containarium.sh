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
		VM_NAME="$2"
		shift 2
		;;
	-h | --help)
		grep '^#' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		die "unknown argument: $1"
		;;
	esac
done
[[ -n "$VM_NAME" ]] || die "usage: $0 --name <vm-name>"

load_config
require_vars K8S_MAX_PODS AGENT_SANDBOX_VERSION CONTAINARIUM_VERSION GVISOR_RUNTIME_CLASS REMOTE_SSH_USER BENCH_SSH_KEY_FILE
resolve_remote "$VM_NAME"

log "provisioning Containarium (pod -> gVisor -> containarium) on '${VM_NAME}'"
provision_base_k8s
install_gvisor

log "installing Helm"
ssh_or_local "curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | sudo bash"

log "resolving Containarium ${CONTAINARIUM_VERSION} and fetching the chart + CLI"
ssh_or_local "sudo bash -s" <<REMOTE
set -euo pipefail
# jq is needed below to resolve "latest" — install it FIRST. Found live:
# installing it at the end of this same block (after it's already used)
# means "latest" resolution silently fails (jq: command not found breaks
# the curl|jq pipe, which then makes curl itself fail with "Failure
# writing output to destination" — a confusing secondary symptom of the
# real, primary cause).
apt-get install -y -qq jq git >/dev/null

TAG="${CONTAINARIUM_VERSION}"
if [[ "\$TAG" == "latest" ]]; then
	TAG=\$(curl -fsSL https://api.github.com/repos/FootprintAI/Containarium/releases/latest | jq -r .tag_name)
fi
echo "resolved tag: \$TAG"
echo "\$TAG" >/tmp/containarium-resolved-tag

BASE_URL="https://github.com/FootprintAI/Containarium/releases/download/\${TAG}"
curl -fsSL -o /tmp/containariumd "\${BASE_URL}/containariumd-linux-amd64"
