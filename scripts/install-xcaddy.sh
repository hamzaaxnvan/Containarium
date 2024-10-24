#!/usr/bin/env bash
# Install a pinned, checksum-verified xcaddy (replaces `go install ...@latest`).
# Usage: scripts/install-xcaddy.sh <destination> [OS] [ARCH]
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/xcaddy-version.env"
destination="${1:?Usage: scripts/install-xcaddy.sh <destination> [OS] [ARCH]}"
