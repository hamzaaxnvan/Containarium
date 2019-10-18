#!/bin/bash
#
# Setup a jump server user on a peer node that proxies SSH into an Incus container.
#
# Usage: sudo bash setup-peer-user.sh <username> [sentinel_pubkey]
#
# This creates:
#   1. containarium-shell script (if not already installed)
#   2. Host-level user with containarium-shell as login shell
#   3. Authorized keys (sentinel upstream key + user key from container)
#   4. Sudoers entry for passwordless incus access
#

set -euo pipefail

USERNAME="${1:-}"
# Default sentinel pubkey: empty. Operators MUST pass the second arg
# (or set SENTINEL_PUBKEY env). Pubkeys are not secrets, but the
# `comment` field bundled with them — `<user>@<sentinel-host>` —
# leaks the sentinel hostname per CLAUDE.md.
SENTINEL_PUBKEY="${2:-${SENTINEL_PUBKEY:-}}"

if [[ -z "$USERNAME" ]]; then
    echo "Usage: sudo $0 <username> [sentinel_pubkey]"
    echo "Example: sudo $0 <tenant-username>"
    exit 1
fi

if [[ -z "$SENTINEL_PUBKEY" ]]; then
    echo "Error: sentinel pubkey required — pass as 2nd arg or via SENTINEL_PUBKEY env"
    exit 1
fi

if [[ $EUID -ne 0 ]]; then
    echo "Error: must run as root (use sudo)"
    exit 1
fi

CONTAINER="${USERNAME}-container"

# 1. Install containarium-shell if missing
if [[ ! -f /usr/local/bin/containarium-shell ]]; then
    echo "==> Installing containarium-shell..."
    cat > /usr/local/bin/containarium-shell << 'SHELL'
#!/bin/bash
# containarium-shell: Proxy SSH sessions into Incus containers
USERNAME="$(whoami)"
CONTAINER="${USERNAME}-container"

