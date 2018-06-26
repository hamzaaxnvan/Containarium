#!/bin/bash
#
# Density loop for the experiment group: create Containarium boxes one at a
# time (each scheduled under gVisor via the runtimeClass set in
# 03-provision-containarium.sh) until the shared stopping rule in lib.sh's
# run_density_loop triggers.
#
# The k8s backend parses --cpu/--memory with Kubernetes' native quantity
# parser (resource.ParseQuantity) — see pkg/core/box/k8s/objects.go. So this
# passes SANDBOX_CPU_LIMIT/SANDBOX_MEM_LIMIT straight through, no unit
# conversion needed (unlike the old LXC-backend version of this script).
#
# SANDBOX_CPU_REQUEST/SANDBOX_MEM_REQUEST (#1557) set a *separate* request
# below the limit via `--cpu-request`/`--memory-request`, matching how the
# control group's Sandbox pods are sized (request lower than limit — k8s
# admission packs on requests, not limits). Before #1557 there was no
# request knob on the CLI at all: `create --cpu/--memory` pinned request to
# the same value as the limit, a real fairness asymmetry against
# Containarium that's now fixable — see README.md "Fairness notes" and
# RESULTS.md's #1541/#1557 entries for the density cost that asymmetry
# turned out to have.
#
# Two more things found live, worth knowing before reading this script:
#
# 1. `create`'s default --image ("images:ubuntu/24.04") is an LXC-style
#    reference, not a valid OCI image — it 500s the k8s backend's pod with
#    InvalidImageName. There's no backend-aware default. Explicitly passes
#    Containarium's own agent-box image instead (the same one the Helm
#    chart's agentBox.image value points at), version-tagged to match the
#    daemon build (":latest" 404s on GHCR — only version tags are
#    published) — read from /tmp/containarium-resolved-tag, which
#    03-provision-containarium.sh's tag-resolution step writes.
#
# 2. `containarium list` unconditionally errors on this backend —
#    "incus backend not available on this host" — so it can't be used for
#    readiness or cleanup verification here. Readiness is checked directly
#    via the k8s API instead: every box's pod is named "box" in namespace
#    "tenant-<username>" (confirmed live), so this polls
#    `kubectl get pod -n tenant-<name> box` for phase=Running + container
#    ready. `containarium delete` (unlike `list`) works fine and is still
#    used for cleanup.
#
# Usage:
#   scripts/04-run-density-containarium.sh --name <vm-name>

set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=lib.sh
source ./lib.sh

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
