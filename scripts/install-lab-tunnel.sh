#!/bin/bash
#
# Phase A of the lab pool bring-up: install the binary and run the tunnel
# client only. The handshake auto-promotes this node into the sentinel's
# primary registry for pool=lab; verifying that confirms slices 1-7 work
# end-to-end against production.
#
# Phase B (Incus + daemon + core containers) follows once Phase A is green.
#
# Usage (on the lab node, with sudo):
#   sudo CONTAINARIUM_TUNNEL_TOKEN=<token> \
#        CONTAINARIUM_SENTINEL_ADDR=<host:port> \
#        CONTAINARIUM_PUBLIC_HOSTNAME=<cluster>.example.com \
#        bash install-lab-tunnel.sh
#
# Idempotent: re-running replaces the unit and restarts the service.

set -euo pipefail

if [[ $EUID -ne 0 ]]; then
    echo "Error: this script must be run as root (use sudo)"
    exit 1
fi

BINARY_SRC="${BINARY_SRC:-/tmp/containariumd}"
BINARY_DST="/usr/local/bin/containariumd"

# Lab tunnel token — env-only (no default literal in this OSS-tracked
# file; see CLAUDE.md OSS-disclosure rule). A prior committed literal
# was rotated 2026-05-25.
LAB_TOKEN="${CONTAINARIUM_TUNNEL_TOKEN:-}"
SENTINEL_ADDR="${CONTAINARIUM_SENTINEL_ADDR:-<sentinel-host>:443}"
SPOT_ID="${SPOT_ID:-lab-primary-1}"
POOL="${POOL:-lab}"
# Pool's public hostname — placeholder; the cluster's apex doesn't
# belong in the repo per CLAUDE.md.
PUBLIC_HOSTNAME="${CONTAINARIUM_PUBLIC_HOSTNAME:-<cluster>.example.com}"
PUBLIC_PORT="${PUBLIC_PORT:-443}"

if [[ -z "$LAB_TOKEN" ]]; then
    echo "Error: CONTAINARIUM_TUNNEL_TOKEN env var required"
    exit 1
fi
if [[ "$SENTINEL_ADDR" == *"<sentinel-host>"* ]]; then
    echo "Error: CONTAINARIUM_SENTINEL_ADDR env var required (got placeholder)"
    exit 1
fi
if [[ "$PUBLIC_HOSTNAME" == *"<cluster>.example.com"* ]]; then
    echo "Error: CONTAINARIUM_PUBLIC_HOSTNAME env var required (got placeholder)"
    exit 1
fi

if [[ ! -f "$BINARY_SRC" ]]; then
