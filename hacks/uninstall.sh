#!/bin/bash
# Containarium Uninstallation Script
#
# This script removes Containarium and optionally its dependencies.
#
# Usage:
#   sudo ./hacks/uninstall.sh              # Remove Containarium only
#   sudo ./hacks/uninstall.sh --purge-incus  # Remove Containarium and Incus
#   sudo ./hacks/uninstall.sh --purge-all    # Remove everything including containers

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Configuration
PURGE_INCUS=false
PURGE_ALL=false

# Parse arguments
for arg in "$@"; do
    case $arg in
        --purge-incus)
            PURGE_INCUS=true
            ;;
        --purge-all)
            PURGE_ALL=true
            PURGE_INCUS=true
            ;;
        --help|-h)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  (none)         Remove Containarium only (keep Incus and containers)"
            echo "  --purge-incus  Remove Containarium AND Incus"
            echo "  --purge-all    Remove EVERYTHING (Containarium, Incus, and all containers)"
            echo ""
            exit 0
            ;;
        *)
            echo "Unknown option: $arg"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

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
        exit 1
    fi
}

confirm_action() {
    local message=$1
    echo ""
    echo -e "${YELLOW}⚠️  WARNING: $message${NC}"
    echo ""
    read -p "Are you sure you want to continue? (yes/no): " -r
    echo ""
    if [[ ! $REPLY =~ ^[Yy][Ee][Ss]$ ]]; then
        log_info "Uninstallation cancelled"
        exit 0
    fi
}

stop_containarium_service() {
    log_info "Stopping Containarium service..."

    if systemctl is-active --quiet containarium; then
        systemctl stop containarium
        log_success "Containarium service stopped"
    else
        log_info "Containarium service is not running"
    fi
}

remove_systemd_service() {
    log_info "Removing systemd service..."

    if [ -f /etc/systemd/system/containarium.service ]; then
        systemctl disable containarium 2>/dev/null || true
        rm -f /etc/systemd/system/containarium.service
        systemctl daemon-reload
        log_success "Systemd service removed"
    else
        log_info "Systemd service not found"
    fi
}

remove_containarium_binary() {
    log_info "Removing Containarium binary..."

    removed=false
    if [ -f /usr/local/bin/containariumd ]; then
        rm -f /usr/local/bin/containariumd
        removed=true
    fi
    # #1781 rollout compat symlink: only remove /usr/local/bin/containarium
    # here if it's a symlink (our own, or anyone else's) — never a real file,
    # which some other mechanism placed there and this uninstaller doesn't own.
    if [ -L /usr/local/bin/containarium ]; then
        rm -f /usr/local/bin/containarium
        removed=true
    fi

    if [ "$removed" = "true" ]; then
        log_success "Containarium binary removed"
    else
        log_info "Containarium binary not found"
    fi
}

remove_config_files() {
    log_info "Removing configuration files..."

    if [ -d /etc/containarium ]; then
        rm -rf /etc/containarium
        log_success "Configuration files removed"
    else
        log_info "Configuration directory not found"
    fi

    if [ -f /etc/modules-load.d/containarium.conf ]; then
        rm -f /etc/modules-load.d/containarium.conf
        log_success "Kernel module configuration removed"
    fi
}

remove_data_directory() {
    log_info "Removing data directory..."

    if [ -d /var/lib/containarium ]; then
        rm -rf /var/lib/containarium
        log_success "Data directory removed"
    else
        log_info "Data directory not found"
    fi
