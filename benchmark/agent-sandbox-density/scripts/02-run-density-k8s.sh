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
