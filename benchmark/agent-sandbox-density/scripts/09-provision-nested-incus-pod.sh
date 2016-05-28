#!/bin/bash
#
# Provision the fourth benchmark scenario (#1565): a k8s cluster hosting
# ONE privileged pod that runs a nested `incusd` plus the existing,
# unmodified `containarium daemon --runtime=lxc` inside it. See
# manifests/nested-incus-pod/README.md for what this is and RESULTS.md's
# 2026-08-26 entry for what actually happened when it was run — the
# mechanism works, but per-container cgroup resource limits don't enforce
# in every cluster (a cgroup-namespace/delegation gap, diagnosed but not
# fixed here).
#
# gVisor is NOT installed on this VM's base cluster — the privileged pod
# must run under plain `runc` (unset RuntimeClassName), since Incus
# itself performs mount/cgroup/namespace-creation syscalls against its
# own child containers, exactly what gVisor intercepts and blocks.
#
# CONTAINARIUM_BINARY_URL (optional, config.env): if the resolved
# CONTAINARIUM_VERSION release predates a flag this scenario's daemon
# invocation needs (--disable-{security,pentest,zap}-scanner — the same
# snag the #1558 and sentinel-statefulset entries in RESULTS.md hit),
# build a `containariumd` binary from `main` (CGO_ENABLED=0 GOOS=linux
# GOARCH=amd64 go build -buildvcs=false ./cmd/containariumd), serve it and
# a `<url>.sha256` file over plain HTTP from somewhere this VM's pod can
# reach (its own host IP works — pods can reach the node they're
# scheduled on), and set this to that URL. Leave unset to use the
# resolved release tag's binary as normal.
#
# Usage:
#   scripts/09-provision-nested-incus-pod.sh --name <vm-name>
#
# Assumes the guest is Ubuntu Server 24.04 with passwordless sudo for
# REMOTE_SSH_USER, same as the other provisioning scripts.

set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=lib.sh
source ./lib.sh
# shellcheck source=k8s-common.sh
source ./k8s-common.sh

VM_NAME=""
POD_CPU="16"
POD_MEM="40Gi"
while [[ $# -gt 0 ]]; do
	case "$1" in
	--name)
		VM_NAME="$2"
		shift 2
		;;
