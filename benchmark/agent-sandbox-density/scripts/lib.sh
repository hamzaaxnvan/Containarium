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
	for var in "$@"; do
		if [[ -z "${!var:-}" ]]; then
			missing+=("$var")
		fi
	done
	if [[ ${#missing[@]} -gt 0 ]]; then
		die "required config value(s) not set: ${missing[*]} (check config.env)"
	fi
}

# resolve_remote looks up the live IP of an Incus VM by name and points
# REMOTE_HOST/REMOTE_SSH_PORT/REMOTE_SSH_USER at it, overriding whatever
# static values config.env has. Incus VMs get a DHCP-assigned bridge IP,
# not a fixed NAT-forwarded port, so there's nothing meaningful to put in
# config.env ahead of time — the provisioning/density scripts all take
# --name and call this instead. REMOTE_SSH_USER/BENCH_SSH_KEY_FILE still
# come from config.env (the cloud-init user and the matching private key).
resolve_remote() {
	local vm_name="$1"
	local ip
	ip=$(sudo incus list "$vm_name" -c 4 --format csv 2>/dev/null | grep -oE '^[0-9.]+' || true)
	[[ -n "$ip" ]] || die "couldn't resolve an IP for Incus VM '${vm_name}' — is it running? (incus list)"
	REMOTE_HOST="$ip"
	REMOTE_SSH_PORT="22"
	log "resolved ${vm_name} -> ${REMOTE_HOST}"

	# A TCP port being open (what 00-create-vm.sh's own readiness check
	# uses) doesn't mean sshd will accept a real login yet — cloud-init can
	# still be mid-way through injecting the authorized key, and sshd is
	# sometimes bounced by a later cloud-init stage after the port first
	# opens. Retry an actual command instead of trusting the port alone;
	# hit this exact race live during the first real run.
	log "waiting for SSH to actually accept connections (cloud-init can still be settling)"
	local i
	for i in $(seq 1 40); do
		if ssh -p "$REMOTE_SSH_PORT" -o StrictHostKeyChecking=accept-new -o ConnectTimeout=5 \
			${BENCH_SSH_KEY_FILE:+-i "$BENCH_SSH_KEY_FILE"} \
			"${REMOTE_SSH_USER:+$REMOTE_SSH_USER@}${REMOTE_HOST}" true 2>/dev/null; then
			return 0
