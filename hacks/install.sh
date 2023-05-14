#!/bin/bash
# Containarium Manual Installation Script
#
# This script installs Containarium and all dependencies on a fresh Ubuntu 24.04 system.
# It's an alternative to Terraform for manual or development deployments.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/footprintai/containarium/main/hacks/install.sh | sudo bash
#   or
#   sudo ./hacks/install.sh
#
# Bootstrap a first box in the same step (server-side):
#   curl -fsSL .../install.sh | sudo bash -s -- --quickstart alice
#
#   --quickstart installs everything, then creates the box <name>. The SSH key
#   is OPTIONAL: pass --ssh-pubkey "ssh-ed25519 AAAA… you@laptop" (or --ssh-key
#   <path>) to seed one, or omit it for a keyless platform-managed box you
#   attach a key to later. It does NOT wire SSH / your agent — those configure
#   your LAPTOP, so the installer prints the command to finish there
#   (`containarium quickstart … --server <this-host>`). See
#   `containarium quickstart --help`.

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
CONTAINARIUM_VERSION="${CONTAINARIUM_VERSION:-latest}"
INCUS_VERSION_REQUIRED="6.19"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/containarium"
DATA_DIR="/var/lib/containarium"

# --quickstart bootstrap options (parsed from CLI args; empty = off)
QUICKSTART_NAME=""
QUICKSTART_SSH_KEY=""
QUICKSTART_SSH_PUBKEY=""
QUICKSTART_STACK="fullstack"

# --mtls: open 50051/tcp in ufw for the external mTLS gRPC listener (0 = off)
ENABLE_MTLS=0

# Helper functions
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

    if [ "$ID" = "debian" ]; then
        # Debian 13 (trixie) has been verified: its kernel carries the
        # CVE-2026-98432 fix, and Incus comes from the Zabbly repository.
        case "$VERSION_ID" in
            12|13) log_info "Debian $VERSION_ID is supported" ;;
            *) log_warn "Debian $VERSION_ID is untested. Supported: 12, 13." ;;
        esac
    elif [ "$ID" != "ubuntu" ]; then
        log_warn "This script is designed for Ubuntu and Debian. Detected: $ID"
        log_warn "Continuing anyway, but issues may occur..."
    elif [ "$VERSION_ID" != "24.04" ] && [ "$VERSION_ID" != "22.04" ]; then
        log_warn "Recommended version: Ubuntu 24.04. Detected: $VERSION_ID"
        log_warn "Continuing anyway..."
    fi

    log_success "OS check passed: $PRETTY_NAME"
}

install_dependencies() {
    log_info "Installing system dependencies..."

    apt-get update
    apt-get install -y \
        curl \
        wget \
        gnupg \
        ca-certificates \
        jq

    # ZFS is optional. Debian ships zfsutils-linux only in contrib, and
    # Ubuntu-only helpers such as software-properties-common are not needed.
    # `apt-cache show` succeeds for names other packages refer to, so check the
    # install candidate instead of the package record.
    ZFS_CANDIDATE=$(apt-cache policy zfsutils-linux | awk '/Candidate:/{print $2}')
    if [ -n "$ZFS_CANDIDATE" ] && [ "$ZFS_CANDIDATE" != "(none)" ]; then
        apt-get install -y zfsutils-linux
    else
        log_warn "zfsutils-linux not available on this distro; ZFS storage is unavailable"
    fi

    log_success "System dependencies installed"
}

install_incus() {
    log_info "Checking Incus installation..."

    # Check if Incus is already installed
    if command -v incus &> /dev/null; then
        INCUS_CURRENT_VERSION=$(incus --version | cut -d'-' -f1)
        log_info "Incus already installed: version $INCUS_CURRENT_VERSION"

        # Compare versions
        if [ "$(printf '%s\n' "$INCUS_VERSION_REQUIRED" "$INCUS_CURRENT_VERSION" | sort -V | head -n1)" == "$INCUS_VERSION_REQUIRED" ]; then
            log_success "Incus version is sufficient (>= $INCUS_VERSION_REQUIRED)"
            return 0
        else
            log_warn "Incus version $INCUS_CURRENT_VERSION is too old. Need >= $INCUS_VERSION_REQUIRED"
            log_info "Upgrading Incus..."
        fi
    fi

    # CRITICAL: Remove ALL Ubuntu Incus packages BEFORE adding Zabbly repository
    # This prevents APT from trying to mix packages from both repositories
    log_info "Removing any existing Ubuntu Incus packages to avoid conflicts..."
    apt-get remove -y incus incus-tools incus-client incus-base incus-ui-canonical 2>/dev/null || true
    apt-get autoremove -y 2>/dev/null || true

    log_info "Installing Incus from Zabbly repository..."

    # Add Zabbly repository (use --batch to avoid TTY issues in non-interactive SSH)
    curl -fsSL https://pkgs.zabbly.com/key.asc | gpg --batch --yes --dearmor -o /usr/share/keyrings/zabbly-incus.gpg

    # Detect the distro codename (works on Ubuntu and Debian; lsb_release is
    # not installed by default on Debian cloud images)
    . /etc/os-release
    DISTRO_CODENAME="${VERSION_CODENAME}"

    echo "deb [signed-by=/usr/share/keyrings/zabbly-incus.gpg] https://pkgs.zabbly.com/incus/stable ${DISTRO_CODENAME} main" | \
        tee /etc/apt/sources.list.d/zabbly-incus-stable.list

    # Create APT preference to prioritize Zabbly repository over the distro's own Incus packages
    if [ "$ID" = "debian" ]; then DISTRO_ORIGIN=Debian; else DISTRO_ORIGIN=Ubuntu; fi
    log_info "Configuring APT to prefer Zabbly Incus packages..."
    cat > /etc/apt/preferences.d/zabbly-incus << EOF
Package: incus incus-* *incus*
Pin: origin pkgs.zabbly.com
Pin-Priority: 1000

Package: incus incus-* *incus*
Pin: release o=${DISTRO_ORIGIN}
Pin-Priority: -1
EOF

    # Update package lists with new repository
    apt-get update

    # Install Incus from Zabbly repository
    # Note: incus-tools was replaced by incus-extra in newer versions
    log_info "Installing incus, incus-client, and incus-extra from Zabbly..."
    apt-get install -y incus incus-client incus-extra

    # Verify installation
    INCUS_VERSION=$(incus --version | cut -d'-' -f1)
    log_success "Incus $INCUS_VERSION installed"

    # Check if initialization is needed.
    #
    # A freshly-installed Incus daemon answers `incus info` immediately, but it
    # has NO storage pool or network yet. Testing `incus info` alone therefore
    # misreports a brand-new install as "already initialized", skips
    # `incus admin init`, and leaves the daemon pool-less — so the first
    # container create fails much later with "Storage pool not found". Require a
    # storage pool to exist before treating Incus as initialized.
    if ! incus info &> /dev/null; then
        log_info "Initializing Incus with default settings..."
        incus admin init --auto
        log_success "Incus initialized"
    elif ! incus storage list --format csv 2>/dev/null | grep -q .; then
        log_warn "Incus daemon is up but has no storage pool; initializing..."
        incus admin init --auto
        log_success "Incus initialized"
    else
        log_info "Incus already initialized"
    fi
}

configure_zfs() {
    log_info "Configuring ZFS..."

    # Some kernel flavors (for example the Debian cloud kernel) ship without
    # the ZFS module. Skip ZFS setup there instead of failing the install.
    if ! lsmod | grep -q zfs; then
        log_info "Loading ZFS kernel module..."
        if ! modprobe zfs 2>/dev/null; then
            log_warn "ZFS kernel module not available for $(uname -r); skipping ZFS setup"
            return 0
        fi
    fi

    # Ensure ZFS loads on boot
    if ! grep -q "^zfs$" /etc/modules-load.d/zfs.conf 2>/dev/null; then
        echo "zfs" > /etc/modules-load.d/zfs.conf
        log_success "ZFS module configured to load on boot"
    fi

    log_success "ZFS configured"
}

configure_kernel_modules() {
    log_info "Configuring kernel modules for Docker support..."

    # Required modules for Docker in containers
    MODULES=("overlay" "br_netfilter" "nf_nat")

    for module in "${MODULES[@]}"; do
        if ! lsmod | grep -q "^$module"; then
            log_info "Loading kernel module: $module"
            modprobe "$module"
        fi

        if ! grep -q "^$module$" /etc/modules-load.d/containarium.conf 2>/dev/null; then
            echo "$module" >> /etc/modules-load.d/containarium.conf
        fi
    done

    log_success "Kernel modules configured"
}

install_containarium_binary() {
    log_info "Installing Containarium binary..."

    # Determine architecture
    ARCH=$(uname -m)
    case $ARCH in
        x86_64)
            ARCH="amd64"
            ;;
        aarch64)
            ARCH="arm64"
            ;;
        *)
            log_error "Unsupported architecture: $ARCH"
            exit 1
            ;;
    esac

    # Download binary
    if [ "$CONTAINARIUM_VERSION" == "latest" ]; then
        DOWNLOAD_URL="https://github.com/footprintai/containarium/releases/latest/download/containariumd-linux-${ARCH}"
    else
        DOWNLOAD_URL="https://github.com/footprintai/containarium/releases/download/${CONTAINARIUM_VERSION}/containariumd-linux-${ARCH}"
    fi

    log_info "Downloading from: $DOWNLOAD_URL"

    if ! curl -fsSL "$DOWNLOAD_URL" -o /tmp/containariumd; then
        log_error "Failed to download Containarium binary"
        log_info "Please check if the release exists: https://github.com/footprintai/containarium/releases"
        exit 1
    fi

    # Install binary. #1781 (design doc, Rollout Phase 1): installs as
    # containariumd; the pre-#1780 name keeps working as a compat symlink for
    # any runbook/cron this inventory missed.
    install -m 755 /tmp/containariumd "$INSTALL_DIR/containariumd"
    ln -sf "$INSTALL_DIR/containariumd" "$INSTALL_DIR/containarium"
    rm /tmp/containariumd

    # Verify installation
    INSTALLED_VERSION=$("$INSTALL_DIR/containariumd" version)
    log_success "Containarium installed: $INSTALLED_VERSION"
}

generate_tls_certificates() {
    log_info "Generating TLS certificates for mTLS..."

    # Check if certificates already exist
    if [ -f "$CONFIG_DIR/certs/server.crt" ]; then
        log_info "TLS certificates already exist"
        return 0
    fi

    # Generate certificates
    "$INSTALL_DIR/containariumd" cert generate --output "$CONFIG_DIR/certs"

    log_success "TLS certificates generated: $CONFIG_DIR/certs"
}

setup_jwt_secret() {
    log_info "Setting up JWT secret for REST API..."

    # Create config directory
    mkdir -p "$CONFIG_DIR"
    chmod 700 "$CONFIG_DIR"

    # Generate JWT secret if it doesn't exist
    if [ ! -f "$CONFIG_DIR/jwt.secret" ]; then
        openssl rand -base64 32 > "$CONFIG_DIR/jwt.secret"
        chmod 600 "$CONFIG_DIR/jwt.secret"
        log_success "JWT secret generated: $CONFIG_DIR/jwt.secret"
    else
        log_info "JWT secret already exists: $CONFIG_DIR/jwt.secret"
    fi
}

create_systemd_service() {
    log_info "Creating systemd service via 'containariumd service install'..."

    # The binary manages its own service file, JWT secret generation,
    # systemd reload, enable, and start — all in one command.
    /usr/local/bin/containariumd service install

    log_success "Systemd service created"
}

setup_firewall() {
    log_info "Configuring firewall..."

    # Check if ufw is installed
    if command -v ufw &> /dev/null; then
        # Allow SSH
        ufw allow 22/tcp comment 'SSH' || true

        # Allow gRPC
        # The daemon serves an external gRPC listener only with --mtls; without
        # it 50051 has nothing behind it, so leave the port closed (#3063).
        if [ "${ENABLE_MTLS:-0}" = "1" ]; then
            ufw allow 50051/tcp comment 'Containarium gRPC (mTLS)' || true
        fi

        # Allow REST API
        ufw allow 8080/tcp comment 'Containarium REST API' || true

        # Enable firewall if not already enabled
        if ! ufw status | grep -q "Status: active"; then
            log_warn "UFW is installed but not active. Enable with: sudo ufw enable"
        else
            log_success "Firewall rules configured"
        fi
    else
        log_warn "UFW not installed. Consider installing for security: apt install ufw"
    fi
}

generate_initial_token() {
    log_info "Generating initial admin token..."

    if [ -f "$CONFIG_DIR/jwt.secret" ]; then
        TOKEN=$("$INSTALL_DIR/containariumd" token generate \
            --username admin \
            --roles admin \
            --expiry 720h \
            --secret-file "$CONFIG_DIR/jwt.secret" 2>/dev/null | grep "^eyJ" || echo "")

        if [ -n "$TOKEN" ]; then
            echo "$TOKEN" > "$CONFIG_DIR/admin.token"
            chmod 600 "$CONFIG_DIR/admin.token"
            log_success "Admin token saved to: $CONFIG_DIR/admin.token"
        fi
    fi
}

print_completion_message() {
    echo ""
    echo "═══════════════════════════════════════════════════════════════"
    echo -e "${GREEN}  ✅ Containarium Installation Complete!${NC}"
    echo "═══════════════════════════════════════════════════════════════"
    echo ""
    echo "📦 Installed Components:"
    echo "   • Containarium $(containarium version)"
    echo "   • Incus $(incus --version)"
    echo "   • ZFS kernel module"
    echo ""
    echo "🔧 Configuration:"
    echo "   • Config directory: $CONFIG_DIR"
    echo "   • JWT secret: $CONFIG_DIR/jwt.secret"
    if [ -f "$CONFIG_DIR/admin.token" ]; then
        echo "   • Admin token: $CONFIG_DIR/admin.token"
    fi
    echo "   • Systemd service: /etc/systemd/system/containarium.service"
    echo ""
    echo "🚀 Next Steps:"
    echo ""
    echo "   1. Start the daemon:"
    echo "      sudo systemctl start containarium"
    echo ""
    echo "   2. Enable auto-start on boot:"
    echo "      sudo systemctl enable containarium"
    echo ""
