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
