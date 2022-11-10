#!/bin/bash
#
# Provision the third scenario's VM: Containarium's native LXC/Incus
# "workhorse" backend — no Kubernetes, no gVisor, no pooling. See
# README.md "Third scenario: native LXC workhorse" for why this exists
# and what it isn't (it is NOT a test of the #1488 warm-pool feature —
# SpawnSandbox still serves the Phase 1 cold path as of this writing, see
# #1523 — this measures the same create path #1522/#1527 already cover on
# the k8s backend, just on Containarium's original backend instead).
#
# Runs the daemon in its default mode (core services on: Postgres, Caddy,
# the otel collector) — NOT --standalone — so the fixed overhead matches
# what a real deployment carries, same principle as the k8s side's
# kube-system pods being left in place (README.md "Fairness notes").
#
# Usage:
#   scripts/05-provision-containarium-lxc.sh --name <vm-name>
#
# Assumes the guest is Ubuntu Server 24.04 with passwordless sudo for
# REMOTE_SSH_USER, same as the other provisioning scripts.

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
require_vars CONTAINARIUM_VERSION REMOTE_SSH_USER BENCH_SSH_KEY_FILE
resolve_remote "$VM_NAME"

log "provisioning Containarium native LXC workhorse on '${VM_NAME}' (version=${CONTAINARIUM_VERSION})"

log "installing Incus"
ssh_or_local "sudo bash -s" <<'REMOTE'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
# iptables found missing live too — without it the daemon's
# PassthroughSyncJob logs a retry failure every 5s (same class of gap as
# jq/git elsewhere in this benchmark; the base image is minimal). Whether
