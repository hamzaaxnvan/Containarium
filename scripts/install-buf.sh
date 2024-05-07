#!/usr/bin/env bash
# Install a checksum-verified binary; explicit platform args support bundles.
set -euo pipefail
source "$(dirname "$0")/buf-version.env"
destination="${1:?Usage: scripts/install-buf.sh <destination> [OS] [ARCH]}"
platform="${2:-$(uname -s)}-${3:-$(uname -m)}"
case "$platform" in
  Linux-x86_64) checksum="$BUF_SHA256_LINUX_X86_64" ;;
  Linux-aarch64) checksum="$BUF_SHA256_LINUX_AARCH64" ;;
  Darwin-x86_64) checksum="$BUF_SHA256_DARWIN_X86_64" ;;
  Darwin-arm64) checksum="$BUF_SHA256_DARWIN_ARM64" ;;
  *) echo "Unsupported buf platform: $platform" >&2; exit 1 ;;
esac
