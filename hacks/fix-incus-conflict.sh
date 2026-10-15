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

UBUNTU_CODENAME=$(lsb_release -cs)
echo "deb [signed-by=/usr/share/keyrings/zabbly-incus.gpg] https://pkgs.zabbly.com/incus/stable ${UBUNTU_CODENAME} main" | \
    tee /etc/apt/sources.list.d/zabbly-incus-stable.list

log_info "Configuring APT to prefer Zabbly Incus packages..."
cat > /etc/apt/preferences.d/zabbly-incus << 'EOF'
Package: incus incus-* *incus*
Pin: origin pkgs.zabbly.com
Pin-Priority: 1000

Package: incus incus-* *incus*
Pin: release o=Ubuntu
Pin-Priority: -1
EOF

log_info "Updating package lists..."
apt-get update

log_info "Installing Incus from Zabbly..."
# Note: incus-tools was replaced by incus-extra in newer versions
apt-get install -y incus incus-client incus-extra

# Verify installation
INCUS_VERSION=$(incus --version)
echo ""
log_success "Incus installed: version $INCUS_VERSION"

# Check if initialization is needed
if ! incus info &> /dev/null; then
    log_info "Initializing Incus..."
    incus admin init --auto
    log_success "Incus initialized"
else
    log_info "Incus already initialized"
fi

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo -e "${GREEN}  ✅ Incus Package Conflict Resolved!${NC}"
echo "═══════════════════════════════════════════════════════════════"
echo ""
echo "You can now continue with the Containarium installation:"
echo "  sudo ./hacks/install.sh"
echo ""
