#!/bin/bash
# build-bundle.sh — assembles the air-gapped install bundle tarball.
#
# Per prd/cloud/air-gapped-install-bundle.md (E3a), produces
# `dist/containarium-bundle-<version>-<os>-<arch>.tar.gz`.
#
# Inputs:
#
#   VERSION   release tag (e.g. v0.19.0) — also stamped into VERSION file
#   OS        target OS (linux only today)
#   ARCH      amd64 | arm64
#   REPO_ROOT defaults to the repo root inferred from this script's path
#   BUNDLE_ALLOW_EMPTY_APT=1
#             dev builds only: build even though the cache holds no .deb.
#             The installer then needs --skip-apt. Never set in the release
#             workflow, which also checks the tarball's content
#             (scripts/bundle/check-bundle-content.sh).
#
# Pre-requisites (the Makefile target handles these):
#
#   1. `make build-release` has produced bin/*-${OS}-${ARCH} binaries.
#   2. `scripts/bundle/download-deps.sh` has populated dist/bundle-cache/.
#
# The tarball layout matches the PRD §"What's in the bundle":
#
#   containarium-bundle-<version>-<os>-<arch>/
#   ├── README.md
#   ├── VERSION
#   ├── CHECKSUMS.sha256
#   ├── offline-install.sh
#   ├── bin/{containarium,mcp-server,agent-box,actions-runner-*.tar.gz}
#   ├── sidecars/*.tar
#   ├── toolchains/{go*,node*,pnpm*,buf*,golangci-lint*}
#   ├── apt/*.deb                     # required; see BUNDLE_ALLOW_EMPTY_APT
#   └── docs/{INSTALL.md,VERIFY.md,GHES.md}
#
# No images/ directory (#3061): nothing builds the containarium-base Incus
# image yet, so the directory always shipped empty. It comes back with its
# producer.

set -euo pipefail

# ---- inputs ----
VERSION="${VERSION:-dev}"
OS="${OS:-linux}"
ARCH="${ARCH:-amd64}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/../.." && pwd)}"

DIST_DIR="$REPO_ROOT/dist"
CACHE_DIR="${CACHE_DIR:-$DIST_DIR/bundle-cache}"

# Strip leading 'v' for the VERSION file content; keep it in tarball name
# for consistency with GH-release tags (which are vN.M.P).
VERSION_NO_V="${VERSION#v}"
BUNDLE_NAME="containarium-bundle-${VERSION}-${OS}-${ARCH}"
STAGING="$DIST_DIR/$BUNDLE_NAME"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

log_info()    { echo -e "${BLUE}[bundle]${NC} $1"; }
log_success() { echo -e "${GREEN}[bundle]${NC} $1"; }
log_warn()    { echo -e "${YELLOW}[bundle]${NC} $1"; }
log_error()   { echo -e "${RED}[bundle]${NC} $1" >&2; }

require_file() {
  if [ ! -f "$1" ]; then
    log_error "Required input missing: $1"
    log_error "Hint: $2"
    exit 1
  fi
