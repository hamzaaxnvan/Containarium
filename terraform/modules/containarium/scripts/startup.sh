#!/bin/bash
#
# Containarium GCE Startup Script (regular VM, non-spot)
# This script runs once when the GCE instance first boots.
# It installs and configures Incus, Docker kernel modules, and sets up the jump server.
#

set -euo pipefail

# Logging
exec 1> >(logger -s -t containarium-startup) 2>&1

echo "================================================"
echo "Containarium Setup Starting"
echo "================================================"

# Variables from Terraform template
INCUS_VERSION="${incus_version}"
ADMIN_USERS="${join(",", admin_users)}"
ENABLE_MONITORING="${enable_monitoring}"
FAIL2BAN_WHITELIST="${fail2ban_whitelist_cidr}"
JWT_SECRET="${jwt_secret}"
SENTINEL_AUTH_SECRET="${sentinel_auth_secret}"
SSH_AUTHZ_KEYS_COMMAND="${ssh_authz_keys_command}"
SSH_AUTHZ_KEYS_ENFORCE="${ssh_authz_keys_enforce}"

# System info
echo "System: $(uname -a)"
echo "Ubuntu: $(lsb_release -d | cut -f2)"

# Update system
echo "==> Updating system packages..."
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get upgrade -y

# Install essential packages
echo "==> Installing essential packages..."
DEBIAN_FRONTEND=noninteractive apt-get install -y \
    curl \
    wget \
    git \
    vim \
    htop \
    jq \
    net-tools \
    bridge-utils \
    ca-certificates \
    gnupg \
    lsb-release

# Install and configure fail2ban for SSH protection
echo "==> Installing fail2ban for intrusion prevention..."
DEBIAN_FRONTEND=noninteractive apt-get install -y fail2ban

# Configure fail2ban for SSH
echo "==> Configuring fail2ban..."
cat > /etc/fail2ban/jail.d/sshd.conf <<'EOF'
# Containarium SSH Protection
# Automatically blocks IPs after failed login attempts
[sshd]
enabled = true
port = 22
filter = sshd
logpath = /var/log/auth.log
maxretry = 3
findtime = 600
bantime = 3600
banaction = iptables-multiport
EOF

# Whitelist internal IPs (parameterized for different network environments)
if [ -n "$FAIL2BAN_WHITELIST" ]; then
    cat > /etc/fail2ban/jail.d/containarium-whitelist.conf <<WEOF
# Whitelist internal networks
[DEFAULT]
ignoreip = 127.0.0.1/8 ::1 $FAIL2BAN_WHITELIST
WEOF
fi

# Start and enable fail2ban
systemctl enable fail2ban
systemctl start fail2ban

echo "✓ fail2ban configured and started"
echo "  - Max retries: 3 failed attempts"
echo "  - Find time: 10 minutes"
echo "  - Ban time: 1 hour"

# Install Incus 6.19+ (required for Docker build support)
echo "==> Installing Incus 6.19+ from Zabbly repository..."
# Ubuntu 24.04 default repos only have Incus 6.0.0, which has AppArmor bug (CVE-2025-52881)
# that breaks Docker builds in unprivileged containers. We need 6.19+ from Zabbly.

# Add Zabbly Incus stable repository
if [ ! -f /etc/apt/sources.list.d/zabbly-incus-stable.list ]; then
    echo "Adding Zabbly Incus repository..."
    curl -fsSL https://pkgs.zabbly.com/key.asc | gpg --dearmor -o /usr/share/keyrings/zabbly-incus.gpg
    echo 'deb [signed-by=/usr/share/keyrings/zabbly-incus.gpg] https://pkgs.zabbly.com/incus/stable noble main' | tee /etc/apt/sources.list.d/zabbly-incus-stable.list
    apt-get update
    echo "✓ Zabbly repository added"
fi

# Install or upgrade Incus
if [ -n "$INCUS_VERSION" ]; then
    echo "Installing Incus version: $INCUS_VERSION"
    DEBIAN_FRONTEND=noninteractive apt-get install -y incus="$INCUS_VERSION" incus-tools incus-client
else
    echo "Installing latest stable Incus (6.19+)..."
    DEBIAN_FRONTEND=noninteractive apt-get install -y incus incus-tools incus-client
fi

# Verify Incus installation and version
INSTALLED_VERSION=$(incus --version)
echo "✓ Incus $INSTALLED_VERSION installed successfully"

# Verify minimum version requirement (6.19+)
MAJOR=$(echo "$INSTALLED_VERSION" | cut -d. -f1)
MINOR=$(echo "$INSTALLED_VERSION" | cut -d. -f2)
if [ "$MAJOR" -lt 6 ] || ([ "$MAJOR" -eq 6 ] && [ "$MINOR" -lt 19 ]); then
    echo "⚠ WARNING: Incus $INSTALLED_VERSION is below 6.19"
    echo "  Docker builds may fail due to AppArmor bug (CVE-2025-52881)"
    echo "  Please upgrade to Incus 6.19 or later"
fi

# Install ZFS for disk quota enforcement
echo "==> Installing ZFS for disk quota support..."
if ! command -v zpool &> /dev/null; then
    DEBIAN_FRONTEND=noninteractive apt-get install -y zfsutils-linux
    modprobe zfs
    echo "✓ ZFS installed"
else
    echo "✓ ZFS already installed"
fi

# Initialize Incus with ZFS
echo "==> Initializing Incus with ZFS..."
if [ ! -f /var/lib/incus/.initialized ]; then
    # Create ZFS pool on boot disk for container storage
    # This enables disk quota enforcement
    if ! zpool list incus-local &>/dev/null; then
        # Create a ZFS pool using a file-based vdev
        # This allows ZFS features (quotas, compression) without a separate disk
        mkdir -p /var/lib/incus/disks
        truncate -s 50G /var/lib/incus/disks/incus.img
        zpool create \
            -o ashift=12 \
            -O compression=lz4 \
            -O atime=off \
            -O xattr=sa \
            -m /var/lib/incus/storage \
            incus-local /var/lib/incus/disks/incus.img

        zfs create incus-local/containers
        echo "✓ ZFS pool created (file-backed, 50GB)"
    fi

    # Initialize Incus with ZFS
    cat <<EOF | incus admin init --preseed
config: {}
networks:
- name: incusbr0
  type: bridge
  config:
    ipv4.address: 10.0.3.1/24
    ipv4.nat: "true"
    ipv6.address: none
storage_pools:
- name: default
  driver: zfs
  config:
    source: incus-local/containers
profiles:
- name: default
  devices:
    eth0:
      name: eth0
      network: incusbr0
      type: nic
    root:
      path: /
      pool: default
      type: disk
      size: 20GB
cluster: null
EOF
    touch /var/lib/incus/.initialized
    echo "✓ Incus initialized with ZFS (quotas ENFORCED)"
else
    echo "✓ Incus already initialized"
fi

# Configure Incus networking
echo "==> Configuring Incus network..."
# Ensure bridge is created with proper subnet
if ! incus network show incusbr0 &> /dev/null; then
    incus network create incusbr0 \
        ipv4.address=10.0.3.1/24 \
        ipv4.nat=true \
        ipv6.address=none
    echo "✓ Created incusbr0 bridge (10.0.3.0/24)"
else
    echo "✓ incusbr0 bridge already exists"
fi

# Load kernel modules for Docker support in containers
echo "==> Loading kernel modules for Docker support..."
MODULES=(
    "overlay"
    "br_netfilter"
    "nf_nat"
    "xt_conntrack"
    "ip_tables"
    "iptable_nat"
)

for mod in "$${MODULES[@]}"; do
    if ! lsmod | grep -q "^$mod "; then
        modprobe "$mod"
        echo "$mod" >> /etc/modules-load.d/containarium.conf
        echo "✓ Loaded $mod"
    fi
done

# Configure sysctl for containers and Docker
echo "==> Configuring sysctl for containers..."
cat > /etc/sysctl.d/99-containarium.conf <<EOF
# Enable IP forwarding
net.ipv4.ip_forward = 1
net.ipv6.conf.all.forwarding = 1

# Bridge netfilter
net.bridge.bridge-nf-call-iptables = 1
net.bridge.bridge-nf-call-ip6tables = 1

# Increase inotify limits (for many containers)
fs.inotify.max_user_instances = 1024
fs.inotify.max_user_watches = 524288
EOF

sysctl --system
echo "✓ Sysctl configured"

# Configure SSH for security
echo "==> Hardening SSH configuration..."
cat >> /etc/ssh/sshd_config.d/containarium.conf <<EOF
# Containarium SSH hardening
PasswordAuthentication no
PubkeyAuthentication yes
PermitRootLogin no
X11Forwarding no
MaxAuthTries 3
LoginGraceTime 20s
EOF

systemctl restart sshd
echo "✓ SSH hardened"

# Set up admin users (from Terraform variable)
echo "==> Setting up admin users..."
IFS=',' read -ra USERS <<< "$ADMIN_USERS"
for username in "$${USERS[@]}"; do
    if ! id "$username" &>/dev/null; then
        # Create user without creating a new primary group (use existing or
        # fall back to "users") — a bare useradd tries to auto-create a
        # same-named primary group, which fails outright if a group of that
        # name already exists (e.g. "admin" collides with a pre-existing
        # system group shipped in the stock Ubuntu image; kafeido-infra#41).
        useradd -m -s /bin/bash -N -G sudo "$username" || useradd -m -s /bin/bash -g users -G sudo "$username"
        echo "$username ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/"$username"
        chmod 0440 /etc/sudoers.d/"$username"
        echo "✓ Created user: $username"
    else
        echo "✓ User already exists: $username"
    fi
done

# Install monitoring agents (if enabled)
if [ "$ENABLE_MONITORING" = "true" ]; then
    echo "==> Installing Google Cloud monitoring agent..."
    curl -sSO https://dl.google.com/cloudagents/add-google-cloud-ops-agent-repo.sh
    bash add-google-cloud-ops-agent-repo.sh --also-install
    rm add-google-cloud-ops-agent-repo.sh
    echo "✓ Monitoring agent installed"
fi

# Create containarium directory structure
