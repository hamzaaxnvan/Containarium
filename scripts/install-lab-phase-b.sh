#!/bin/bash
#
# Phase B of the lab pool bring-up: install Incus, initialize it, configure
# the containarium daemon to run with --pool=lab, and let the daemon bring
# up its core containers (postgres, victoriametrics, caddy). After this
# completes successfully, https://lab.example.com/ should
# eventually serve the WebUI (Caddy fetches its Let's Encrypt cert via
# TLS-ALPN-01 over the SNI route established in slice 8).
#
# Idempotent: re-running skips steps already done. Safe to interrupt and
# resume; the daemon's own state survives across runs.
#
# Usage (on the lab node, with sudo):
#   sudo bash install-lab-phase-b.sh
#
# What it does NOT touch:
#   - The existing containarium-tunnel.service (Phase A — leave running)
#   - The /usr/local/bin/containariumd binary (Phase A installed it, #1781)

set -euo pipefail

if [[ $EUID -ne 0 ]]; then
    echo "Error: this script must be run as root (use sudo)"
    exit 1
fi

NETWORK_SUBNET="${NETWORK_SUBNET:-10.0.4.1/24}"
POOL="${POOL:-lab}"
BASE_DOMAIN="${BASE_DOMAIN:-lab.example.com}"

echo "==> Step 1/8: Install Incus from Zabbly"
if command -v incus >/dev/null 2>&1; then
    echo "    Already installed: $(incus version | head -1)"
else
    install -d -m 0755 /etc/apt/keyrings
    curl -fsSL https://pkgs.zabbly.com/key.asc | gpg --dearmor -o /etc/apt/keyrings/zabbly.gpg
    cat > /etc/apt/sources.list.d/zabbly-incus-stable.sources <<EOF
Enabled: yes
Types: deb
URIs: https://pkgs.zabbly.com/incus/stable
Suites: $(lsb_release -cs)
Components: main
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/zabbly.gpg
EOF
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y incus
    echo "    Installed: $(incus version | head -1)"
fi

echo
echo "==> Step 2/8: Initialize Incus (default storage + network)"
if incus storage list -f csv 2>/dev/null | grep -q '^default,'; then
    echo "    Already initialized — storage 'default' exists"
else
    # --auto picks dir backend when ZFS/btrfs isn't available, which
    # matches what we want on this VirtualBox guest. The default network
    # name is incusbr0, which the daemon expects.
    incus admin init --auto --network-address=127.0.0.1 --network-port=8443 --storage-backend=dir
    echo "    Storage + network initialized"
fi

echo
echo "==> Step 3/8: JWT secret for the daemon's REST API"
if [[ ! -s /etc/containarium/jwt.secret ]]; then
    install -d -m 0700 /etc/containarium
    openssl rand -hex 32 > /etc/containarium/jwt.secret
    chmod 600 /etc/containarium/jwt.secret
    echo "    Generated: /etc/containarium/jwt.secret"
else
    echo "    Already exists: /etc/containarium/jwt.secret"
fi

echo
