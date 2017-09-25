#!/usr/bin/env bash
# Install a pinned, checksum-verified xcaddy (replaces `go install ...@latest`).
# Usage: scripts/install-xcaddy.sh <destination> [OS] [ARCH]
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/xcaddy-version.env"
destination="${1:?Usage: scripts/install-xcaddy.sh <destination> [OS] [ARCH]}"
platform="${2:-$(uname -s)}-${3:-$(uname -m)}"
case "$platform" in
  Linux-x86_64)  asset=linux_amd64; checksum="$XCADDY_SHA256_LINUX_X86_64" ;;
  Linux-aarch64) asset=linux_arm64; checksum="$XCADDY_SHA256_LINUX_AARCH64" ;;
  Darwin-x86_64) asset=mac_amd64;   checksum="$XCADDY_SHA256_DARWIN_X86_64" ;;
  Darwin-arm64)  asset=mac_arm64;   checksum="$XCADDY_SHA256_DARWIN_ARM64" ;;
