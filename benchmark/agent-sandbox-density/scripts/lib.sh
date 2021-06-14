#!/bin/bash
#
# Shared helpers for the sandbox-density benchmark scripts. Sourced, not
# executed directly.
#
# Keeping the stopping rule and the resource-snapshot format here (instead
# of duplicated in 02-run-density-k8s.sh and 04-run-density-containarium.sh)
# is what makes the two sides comparable — a change to the methodology only
# has to be made once. See README.md "Fairness notes".

set -euo pipefail

BENCH_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

log() {
	echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] $*" >&2
}

die() {
	log "ERROR: $*"
	exit 1
}

# load_config sources config.env (created from config.env.example) and
# fails loudly if it's missing, rather than silently running with unset
# variables.
load_config() {
	local config_file="${BENCH_ROOT}/config.env"
	[[ -f "$config_file" ]] || die "missing $config_file — copy config.env.example to config.env and fill it in first"
	# shellcheck source=/dev/null
	source "$config_file"
}

# require_vars checks that every named variable is non-empty, failing
# loudly and naming which one is missing rather than proceeding with an
# empty value that fails confusingly three steps later.
require_vars() {
	local missing=()
	local var
