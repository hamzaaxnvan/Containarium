#!/bin/bash
#
# Setup Caddy with DNS provider plugin for wildcard TLS
#
# Usage:
#   ./setup-caddy.sh --dns cloudflare --domain containarium.dev --email admin@example.com
#   ./setup-caddy.sh --dns route53 --domain myplatform.io --email admin@example.com
#

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Default values
DNS_PROVIDER=""
DOMAIN=""
EMAIL=""
CADDY_USER="caddy"
CADDY_BIN="/usr/local/bin/caddy"
CADDYFILE="/etc/caddy/Caddyfile"
INSTALL_DIR="/tmp/caddy-build"

# DNS provider to module mapping
declare -A DNS_MODULES=(
    ["cloudflare"]="github.com/caddy-dns/cloudflare"
    ["route53"]="github.com/caddy-dns/route53"
    ["googleclouddns"]="github.com/caddy-dns/googleclouddns"
    ["digitalocean"]="github.com/caddy-dns/digitalocean"
    ["azure"]="github.com/caddy-dns/azure"
    ["vultr"]="github.com/caddy-dns/vultr"
    ["duckdns"]="github.com/caddy-dns/duckdns"
    ["namecheap"]="github.com/caddy-dns/namecheap"
    ["godaddy"]="github.com/caddy-dns/godaddy"
)

# DNS provider to environment variable mapping
declare -A DNS_ENV_VARS=(
    ["cloudflare"]="CF_API_TOKEN"
    ["route53"]="AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY"
    ["googleclouddns"]="GCP_PROJECT, GOOGLE_APPLICATION_CREDENTIALS"
    ["digitalocean"]="DO_AUTH_TOKEN"
    ["azure"]="AZURE_TENANT_ID, AZURE_CLIENT_ID, AZURE_CLIENT_SECRET"
    ["vultr"]="VULTR_API_KEY"
    ["duckdns"]="DUCKDNS_API_TOKEN"
    ["namecheap"]="NAMECHEAP_API_USER, NAMECHEAP_API_KEY"
    ["godaddy"]="GODADDY_API_TOKEN"
)

print_usage() {
    echo "Usage: $0 --dns <provider> --domain <domain> --email <email>"
    echo ""
    echo "Options:"
    echo "  --dns       DNS provider (cloudflare, route53, googleclouddns, digitalocean, azure, vultr, duckdns, namecheap, godaddy)"
    echo "  --domain    Base domain for app hosting (e.g., containarium.dev)"
    echo "  --email     Email for Let's Encrypt notifications"
    echo "  --help      Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0 --dns cloudflare --domain containarium.dev --email admin@example.com"
    echo "  $0 --dns route53 --domain myplatform.io --email devops@company.com"
}

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

check_root() {
    if [[ $EUID -ne 0 ]]; then
        log_error "This script must be run as root (use sudo)"
        exit 1
    fi
}

check_dependencies() {
    log_info "Checking dependencies..."

    # Check for Go
    if ! command -v go &> /dev/null; then
        log_error "Go is required but not installed. Please install Go 1.21+ first."
        exit 1
    fi

    GO_VERSION=$(go version | grep -oP 'go\d+\.\d+' | grep -oP '\d+\.\d+')
    log_info "Found Go version: $GO_VERSION"
}

install_xcaddy() {
    log_info "Installing xcaddy..."

    # Install xcaddy
    # Pinned, checksum-verified release binary (scripts/xcaddy-version.env).
    "$(dirname "${BASH_SOURCE[0]}")/install-xcaddy.sh" /usr/local/bin/xcaddy

    if ! command -v xcaddy &> /dev/null; then
        log_error "xcaddy installation failed"
        exit 1
    fi

    log_info "xcaddy installed successfully"
}

build_caddy() {
    local dns_module="${DNS_MODULES[$DNS_PROVIDER]}"

    if [[ -z "$dns_module" ]]; then
        log_error "Unknown DNS provider: $DNS_PROVIDER"
        log_error "Supported providers: ${!DNS_MODULES[*]}"
        exit 1
    fi

    log_info "Building Caddy with $DNS_PROVIDER DNS plugin..."

    mkdir -p "$INSTALL_DIR"
    cd "$INSTALL_DIR"

    xcaddy build --with "$dns_module" --output "$CADDY_BIN"

    if [[ ! -f "$CADDY_BIN" ]]; then
        log_error "Caddy build failed"
        exit 1
    fi

    chmod +x "$CADDY_BIN"
    log_info "Caddy built successfully at $CADDY_BIN"

    # Verify DNS module is included
    if $CADDY_BIN list-modules | grep -q "dns.providers"; then
        log_info "DNS provider module verified"
    else
        log_warn "DNS provider module not found in build"
    fi
}

create_caddy_user() {
    if id "$CADDY_USER" &>/dev/null; then
        log_info "User $CADDY_USER already exists"
    else
        log_info "Creating caddy user..."
        useradd --system --home /var/lib/caddy --shell /usr/sbin/nologin $CADDY_USER
        mkdir -p /var/lib/caddy
        chown -R $CADDY_USER:$CADDY_USER /var/lib/caddy
    fi
}

create_caddyfile() {
    log_info "Creating Caddyfile..."

    mkdir -p /etc/caddy

    # Determine the DNS config syntax based on provider
    local dns_config=""
    case $DNS_PROVIDER in
        cloudflare)
            dns_config="dns cloudflare {env.CF_API_TOKEN}"
            ;;
        route53)
            dns_config="dns route53"
            ;;
        googleclouddns)
            dns_config="dns googleclouddns {env.GCP_PROJECT}"
            ;;
        digitalocean)
            dns_config="dns digitalocean {env.DO_AUTH_TOKEN}"
            ;;
        azure)
            dns_config="dns azure"
            ;;
        vultr)
            dns_config="dns vultr {env.VULTR_API_KEY}"
