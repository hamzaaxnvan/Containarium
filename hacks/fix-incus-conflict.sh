#!/bin/bash
# Fix Incus package conflict between Ubuntu and Zabbly repositories
#
# This script resolves the common issue where Ubuntu's default Incus (6.0.0)
# conflicts with Zabbly's newer version (6.19+)

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

if [ "$EUID" -ne 0 ]; then
    log_error "This script must be run as root"
    exit 1
fi

echo "═══════════════════════════════════════════════════════════════"
echo "  Fixing Incus Package Conflict"
echo "═══════════════════════════════════════════════════════════════"
echo ""

log_info "Removing Ubuntu's default Incus packages..."
apt-get remove -y incus incus-tools incus-client incus-base 2>/dev/null || true

log_info "Cleaning up package database..."
apt-get autoremove -y
apt-get autoclean

log_info "Adding Zabbly repository..."
curl -fsSL https://pkgs.zabbly.com/key.asc | gpg --batch --yes --dearmor -o /usr/share/keyrings/zabbly-incus.gpg
