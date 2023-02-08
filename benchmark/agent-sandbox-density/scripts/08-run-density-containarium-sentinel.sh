#!/bin/bash
#
# Density loop for the sentinel-statefulset scenario — identical to
# 04-run-density-containarium.sh (same sandbox profile via #1557's
# --memory-request/--cpu-request, same readiness check, same stopping
# rule), with exactly one difference: CTN_SERVER points at the sentinel
# Service instead of the daemon Service directly, so every `containarium
# create` call actually traverses the Service -> Deployment(sentinel) ->
# StatefulSet(daemon) hop documented in README.md's "The experiment
# group's k8s footprint" / manifests/sentinel-statefulset/README.md.
#
# Compare this run's result directly against the 373 baseline from
# 04-run-density-containarium.sh (same host, same profile, no sentinel
# hop) — see RESULTS.md.
#
# Usage:
#   scripts/08-run-density-containarium-sentinel.sh --name <vm-name>

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

load_config
require_vars SANDBOX_CPU_LIMIT SANDBOX_MEM_LIMIT SANDBOX_CPU_REQUEST SANDBOX_MEM_REQUEST FAILURE_STREAK_TO_STOP CREATE_TIMEOUT_SECONDS BENCH_SSH_KEY_FILE
resolve_remote "$VM_NAME"

log "sandbox profile: cpu=${SANDBOX_CPU_REQUEST}/${SANDBOX_CPU_LIMIT} memory=${SANDBOX_MEM_REQUEST}/${SANDBOX_MEM_LIMIT} (request/limit) via the sentinel hop"

RESULTS_DIR="${BENCH_ROOT}/results"
mkdir -p "$RESULTS_DIR"
RESULTS_FILE="${RESULTS_DIR}/containarium-sentinel-$(date -u +%Y%m%dT%H%M%SZ).md"

RESOLVED_TAG=$(ssh_or_local "cat /tmp/containarium-resolved-tag 2>/dev/null" || true)
[[ -n "$RESOLVED_TAG" ]] || die "couldn't read /tmp/containarium-resolved-tag — did 07-provision-containarium-sentinel.sh run first?"
AGENT_BOX_IMAGE="ghcr.io/footprintai/containarium-agent-box:${RESOLVED_TAG}"
log "using agent-box image ${AGENT_BOX_IMAGE}"

log "resolving the SENTINEL's ClusterIP (not the daemon's — this is the whole point of this scenario) and minting an admin token"
CTN_CLUSTERIP=$(ssh_or_local "kubectl get svc containarium-bench-sentinel -o jsonpath='{.spec.clusterIP}'")
