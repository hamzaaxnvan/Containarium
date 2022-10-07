#!/bin/bash
# download-deps.sh — pulls toolchain tarballs + .deb packages into the
# bundle staging directory, for the air-gapped install bundle.
#
# Per prd/cloud/air-gapped-install-bundle.md (E3a), the bundle ships:
#
#   - Go toolchain (runner kit uses it for buf install + Go CI jobs)
#   - Node + pnpm (runner kit uses it for frontend builds)
#   - buf (proto codegen)
#   - golangci-lint (Go linting)
#   - actions-runner (GitHub Actions self-hosted runner binary)
#   - Incus debs + ZFS utils + base apt deps (no internet during install)
#
# Required env (or default):
#
#   STAGING_DIR    where to write tarballs (default: dist/bundle-cache)
#   OS             linux (only OS supported today)
#   ARCH           amd64 | arm64
#   GO_VERSION     pinned in the script (override if bumping)
#   NODE_VERSION
#   PNPM_VERSION
#   BUF_VERSION     pinned in scripts/buf-version.env (not overridden)
#   GOLANGCI_LINT_VERSION
#   RUNNER_VERSION
#   DEPS_ONLY      all (default) | toolchains | apt — run only one half,
#                  e.g. toolchains on a non-Debian laptop, apt on an Ubuntu
#                  builder
#   APT_PACKAGES   space-separated packages to bundle with their dependency
#                  closure (default: the list below)
#   APT_BATCH_SIZE packages per `apt-get download` call (1-500, default 25;
#                  anything else fails fast); each
#                  batch is logged, so a stall shows which batch it is in
#   GITHUB_STEP_SUMMARY  when set (GitHub Actions), the step's result,
#                  duration and package count are appended to it
#
# Outputs (under $STAGING_DIR):
#
#   toolchains/go${GO_VERSION}.${OS}-${ARCH}.tar.gz
#   toolchains/node-v${NODE_VERSION}-${OS}-${node_arch}.tar.xz
#   toolchains/pnpm-${PNPM_VERSION}-${OS}-${node_arch}
#   toolchains/buf-${OS}-${buf_arch}
#   toolchains/golangci-lint-${GOLANGCI_LINT_VERSION}-${OS}-${ARCH}.tar.gz
#   bin/actions-runner-${OS}-${runner_arch}-${RUNNER_VERSION}.tar.gz
#   apt/*.deb
#
# Idempotent — re-runs skip already-downloaded files (checks file
# existence; does NOT re-validate checksums beyond what curl returns).
#
# NOTE on apt downloads: requires `apt-get` and `dpkg-deb` on an Ubuntu host
# of the target release (after `apt-get update`), and
# the Zabbly repo for incus (scripts/bundle/add-zabbly-repo.sh). Any apt error,
# a missing tool, or a dependency the bundle cannot satisfy fails the script
# (#3061). See scripts/bundle/README.md.

set -euo pipefail

# ---- defaults ----
STAGING_DIR="${STAGING_DIR:-dist/bundle-cache}"
OS="${OS:-linux}"
ARCH="${ARCH:-amd64}"

# Toolchain versions — pin precisely per PRD reproducibility requirement.
GO_VERSION="${GO_VERSION:-1.25.10}"
NODE_VERSION="${NODE_VERSION:-22.11.0}"
PNPM_VERSION="${PNPM_VERSION:-9.12.3}"
source "$(dirname "$0")/../buf-version.env"
GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-2.1.6}"
RUNNER_VERSION="${RUNNER_VERSION:-2.319.1}"

DEPS_ONLY="${DEPS_ONLY:-all}"
case "$DEPS_ONLY" in
  all|toolchains|apt) ;;
  *) echo "DEPS_ONLY must be all, toolchains or apt (got: $DEPS_ONLY)" >&2; exit 1 ;;
esac

# Fail fast, never silently fall back: an out-of-range value is a typo or a
# broken caller, and quietly using 25 would hide it. Unset or empty keeps the
# default. The check is a regex on the string, before any arithmetic: a
# 2^64-ish value wraps to 0 (division by zero) and 2^63-1 yields a negative
# batch count. Max 500 (three digits): each `apt-get download` argument is
# ~40 bytes, so 500 is ~20 KB, far below ARG_MAX (~2 MB), and large enough that
# the default closure is a single batch while a stall still shows its batch.
APT_BATCH_SIZE="${APT_BATCH_SIZE:-25}"
if ! [[ "$APT_BATCH_SIZE" =~ ^[1-9][0-9]{0,2}$ ]] || [ "$APT_BATCH_SIZE" -gt 500 ]; then
  printf 'APT_BATCH_SIZE must be an integer from 1 to 500 (got: %q)\n' "${APT_BATCH_SIZE:0:40}" >&2
  exit 1
fi

# What install.sh would apt-get install on a connected host: runner-kit deps,
# ZFS, and incus/incus-client/incus-extra from the Zabbly stable repo.
APT_PACKAGES="${APT_PACKAGES:-curl wget gnupg ca-certificates software-properties-common jq zfsutils-linux build-essential libicu-dev git incus incus-client incus-extra}"

# Network bounds for every apt call (#3118). Without them one stalled HTTPS
# connection held `apt-get download` silent for 46 minutes. A connection idle
# for 30s now fails and is retried 3 times; apt's error then names the URL.
APT_NET_OPTS=(-o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 -o Acquire::Retries=3)
APT_PKG_COUNT=""   # resolved package count, for the job summary

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

log_info()    { echo -e "${BLUE}[deps]${NC} $1"; }
log_success() { echo -e "${GREEN}[deps]${NC} $1"; }
log_warn()    { echo -e "${YELLOW}[deps]${NC} $1"; }
log_error()   { echo -e "${RED}[deps]${NC} $1" >&2; }

# Map ARCH → vendor-specific naming conventions.
# Go uses linux-amd64 / linux-arm64.
# Node uses linux-x64 / linux-arm64.
# buf uses Linux-x86_64 / Linux-aarch64.
# golangci-lint uses linux-amd64 / linux-arm64.
# actions-runner uses linux-x64 / linux-arm64.
case "$ARCH" in
  amd64)
    NODE_ARCH="x64"
    BUF_ARCH="x86_64"
    RUNNER_ARCH="x64"
    ;;
  arm64)
    NODE_ARCH="arm64"
    BUF_ARCH="aarch64"
    RUNNER_ARCH="arm64"
    ;;
  *)
    log_error "Unsupported ARCH: $ARCH (expected amd64 or arm64)"
    exit 1
    ;;
esac

# Capitalised OS for buf (Linux vs linux).
case "$OS" in
  linux)
    BUF_OS="Linux"
    ;;
  *)
    log_error "Unsupported OS: $OS (expected linux)"
    exit 1
