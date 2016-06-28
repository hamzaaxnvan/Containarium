#!/bin/bash
#
# Density loop for the control group: create Sandbox CRs (agents.x-k8s.io/
# v1beta1) one at a time via the upstream agent-sandbox controller until the
# shared stopping rule in lib.sh's run_density_loop triggers.
#
# Usage:
#   scripts/02-run-density-k8s.sh --name <vm-name>
#
# NOTE: the Sandbox CR's Ready-status field is asserted here as
# `.status.conditions[?(@.type=="Ready")].status` — the common controller
# convention, but NOT yet verified against a live agent-sandbox-controller
# run (this benchmark hasn't been executed yet — see README.md "Running
# it"). On the first real run, sanity-check with
# `kubectl get sandbox <name> -n sandbox-density-bench -o yaml` and adjust
# `sandbox_ready` below if the actual schema differs.

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
require_vars SANDBOX_CPU_REQUEST SANDBOX_CPU_LIMIT SANDBOX_MEM_REQUEST SANDBOX_MEM_LIMIT \
	GVISOR_RUNTIME_CLASS FAILURE_STREAK_TO_STOP CREATE_TIMEOUT_SECONDS BENCH_SSH_KEY_FILE
resolve_remote "$VM_NAME"

NAMESPACE="sandbox-density-bench"
RESULTS_DIR="${BENCH_ROOT}/results"
mkdir -p "$RESULTS_DIR"
RESULTS_FILE="${RESULTS_DIR}/k8s-$(date -u +%Y%m%dT%H%M%SZ).md"

create_sandbox() {
	local name="$1"
	sed \
		-e "s/__NAME__/${name}/g" \
		-e "s/__SANDBOX_CPU_REQUEST__/${SANDBOX_CPU_REQUEST}/g" \
		-e "s/__SANDBOX_CPU_LIMIT__/${SANDBOX_CPU_LIMIT}/g" \
		-e "s/__SANDBOX_MEM_REQUEST__/${SANDBOX_MEM_REQUEST}/g" \
		-e "s/__SANDBOX_MEM_LIMIT__/${SANDBOX_MEM_LIMIT}/g" \
		-e "s/__GVISOR_RUNTIME_CLASS__/${GVISOR_RUNTIME_CLASS}/g" \
		"${BENCH_ROOT}/manifests/sandbox-template.yaml" |
		ssh_or_local "kubectl apply -f -" >/dev/null 2>&1
}

sandbox_ready() {
