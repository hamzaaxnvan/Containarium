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
