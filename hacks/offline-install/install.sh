#!/bin/bash
# Containarium Air-Gapped Installation Script
#
# This script installs Containarium and all dependencies on a fresh
# Ubuntu 24.04 system WITHOUT making any network calls. Every dependency
# is read from the surrounding bundle directory tree.
#
# Per prd/cloud/air-gapped-install-bundle.md (E3a/E3b), this is the
# drop-in replacement for `hacks/install.sh` for Tier 3 customers with
# no internet egress.
#
# Run from the extracted bundle directory:
#
#   tar xzf containarium-bundle-vX.Y.Z-linux-amd64.tar.gz
#   cd containarium-bundle-vX.Y.Z-linux-amd64/
#   sudo ./offline-install.sh
#
# Flags (match hacks/install.sh where applicable):
#
#   --skip-checksum-verify    skip re-checking CHECKSUMS.sha256 (NOT
#                             recommended; we re-verify by default
#                             because in-transit corruption is the
#                             primary integrity threat for air-gapped
#                             distribution).
#   --skip-apt                don't install the bundled .deb packages;
#                             Incus, ZFS, jq and curl must already be on
#                             the host. Without it an apt/ with no .deb
#                             is an error.
#   --skip-sidecars           don't import sidecar images (do it later)
#   --skip-firewall           don't touch ufw rules
#   --skip-base-image         accepted and ignored: the bundle ships no
#                             base image (#3061)
#   --mtls                    open 50051/tcp in ufw for the external mTLS gRPC
#                             listener (the daemon must also run with --mtls)

set -e

# Colors for output — match hacks/install.sh
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration (match hacks/install.sh)
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/containarium"
DATA_DIR="/var/lib/containarium"

# Bundle layout — every path is RESOLVED FROM the bundle directory
# (this script's parent), never from the network.
BUNDLE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$BUNDLE_DIR/bin"
APT_DIR="$BUNDLE_DIR/apt"
SIDECAR_DIR="$BUNDLE_DIR/sidecars"
CHECKSUM_FILE="$BUNDLE_DIR/CHECKSUMS.sha256"
VERSION_FILE="$BUNDLE_DIR/VERSION"

# Flag defaults
SKIP_CHECKSUM_VERIFY=0
SKIP_APT=0
SKIP_SIDECARS=0
SKIP_FIREWALL=0
ENABLE_MTLS=0

# ---- argparse ----
while [ $# -gt 0 ]; do
  case "$1" in
    --skip-checksum-verify) SKIP_CHECKSUM_VERIFY=1 ;;
    --skip-apt)             SKIP_APT=1 ;;
    --skip-sidecars)        SKIP_SIDECARS=1 ;;
    --skip-base-image)      echo "--skip-base-image: ignored, this bundle ships no base image" >&2 ;;
    --skip-firewall)        SKIP_FIREWALL=1 ;;
    --mtls)                 ENABLE_MTLS=1 ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//' | head -50
      exit 0
      ;;
    *)
      echo "Unknown flag: $1 (use --help)" >&2
      exit 1
      ;;
  esac
  shift
done

# Helper functions (match hacks/install.sh)
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

check_root() {
    if [ "$EUID" -ne 0 ]; then
        log_error "This script must be run as root"
        log_info "Please run: sudo $0"
        exit 1
    fi
}

check_os() {
    log_info "Checking operating system..."

    if [ ! -f /etc/os-release ]; then
        log_error "Cannot detect OS. /etc/os-release not found."
        exit 1
    fi

    . /etc/os-release

    if [ "$ID" != "ubuntu" ]; then
        log_warn "This script is designed for Ubuntu. Detected: $ID"
        log_warn "Continuing anyway, but issues may occur..."
    fi

    if [ "$VERSION_ID" != "24.04" ] && [ "$VERSION_ID" != "22.04" ]; then
        log_warn "Recommended version: Ubuntu 24.04. Detected: $VERSION_ID"
        log_warn "Continuing anyway..."
    fi

    log_success "OS check passed: $PRETTY_NAME"
}

check_bundle() {
    log_info "Verifying bundle layout at $BUNDLE_DIR"

    local errors=0
    for required in "$BIN_DIR/containariumd" "$BIN_DIR/mcp-server" "$BIN_DIR/agent-box" "$VERSION_FILE"; do
        if [ ! -f "$required" ]; then
            log_error "Missing bundle file: $required"
            errors=$((errors + 1))
        fi
    done

    if [ $errors -gt 0 ]; then
        log_error "Bundle is incomplete. Did you extract the full tarball?"
        exit 1
    fi

    log_success "Bundle layout OK (version $(cat "$VERSION_FILE"))"
}

verify_checksums() {
    if [ "$SKIP_CHECKSUM_VERIFY" = "1" ]; then
        log_warn "Skipping checksum verification (--skip-checksum-verify)"
        return 0
    fi

    if [ ! -f "$CHECKSUM_FILE" ]; then
        log_warn "CHECKSUMS.sha256 not found — skipping verification."
        log_warn "Recommend re-downloading the bundle if this is unexpected."
        return 0
    fi

    log_info "Verifying bundle SHA256 checksums (catches in-transit corruption)..."
    if (cd "$BUNDLE_DIR" && sha256sum -c CHECKSUMS.sha256 --quiet); then
        log_success "All bundle files match recorded checksums"
    else
        log_error "Checksum mismatch — bundle is corrupted or tampered with"
        log_error "Re-transfer the bundle from the source"
        exit 1
    fi
}

install_apt_dependencies() {
    if [ "$SKIP_APT" = "1" ]; then
        log_info "Skipping bundled .deb install (--skip-apt); Incus, ZFS, jq and curl must already be installed"
        return 0
    fi

    log_info "Installing system dependencies from $APT_DIR (no network)"

    # An empty apt/ is an error, not a skip (#3061): carrying on installed
    # Containarium without the packages it needs.
    shopt -s nullglob
    local debs=("$APT_DIR"/*.deb)
    shopt -u nullglob
    if [ ${#debs[@]} -eq 0 ]; then
        log_error "No .deb packages in the bundle's apt/ directory ($APT_DIR)"
        log_error "This bundle cannot install Incus, ZFS, jq or curl on an air-gapped host."
        log_error "If they are already installed, re-run with --skip-apt."
        exit 1
    fi

    # With --no-download apt only uses archives already in its cache. Local
    # file arguments alone do not populate that cache: on a fresh host apt
    # handed dpkg a bare filename and failed (#3258). Use the verified bundle
    # itself as the archive cache, keeping downloads disabled.
    log_info "Running: apt-get install --no-download --yes (archive cache: $APT_DIR)"
    if ! apt-get -o "Dir::Cache::archives=$APT_DIR" install --no-download --yes "${debs[@]}"; then
        log_error "Failed to install bundled .deb packages; stopping offline installation"
        exit 1
    fi

    # Verify the critical pieces landed.
    local missing=()
    for cmd in incus zfs jq curl; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            missing+=("$cmd")
        fi
    done

    if [ ${#missing[@]} -gt 0 ]; then
        log_error "Required commands missing after apt step: ${missing[*]}"
        log_error "Check /var/log/dpkg.log; bundle may be missing transitive deps"
        exit 1
    fi

    log_success "System dependencies installed from bundle"
}

initialize_incus() {
    log_info "Checking Incus initialization..."

    if ! incus info &> /dev/null; then
        log_info "Initializing Incus with default settings..."
        incus admin init --auto
        log_success "Incus initialized"
    else
        log_info "Incus already initialized"
    fi
}

configure_zfs() {
    log_info "Configuring ZFS..."

    if ! lsmod | grep -q zfs; then
        log_info "Loading ZFS kernel module..."
        modprobe zfs || log_warn "modprobe zfs failed; kernel may not have ZFS support"
    fi

    if ! grep -q "^zfs$" /etc/modules-load.d/zfs.conf 2>/dev/null; then
        echo "zfs" > /etc/modules-load.d/zfs.conf
        log_success "ZFS module configured to load on boot"
    fi

    log_success "ZFS configured"
