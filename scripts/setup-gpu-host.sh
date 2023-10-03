#!/bin/bash
#
# Containarium GPU Host Setup Script
#
# Sets up a bare-metal or non-GCE Linux server with:
#   1. NVIDIA driver (pinned version)
#   2. NVIDIA Container Toolkit (for Incus GPU passthrough)
#   3. Incus (from Zabbly, pinned version)
#   4. ZFS storage backend
#   5. Kernel modules and sysctl for containers
#
# Usage:
#   sudo ./setup-gpu-host.sh [--skip-reboot] [--yes] [--data-disk DISK] [--backup-disks DISK1,DISK2] [--zfs-encryption-keyfile PATH]
#
# Disk layout (auto-detected if not specified):
#   --data-disk                 Fastest unused disk for container storage (e.g., nvme0n1)
#   --backup-disks              Rotational disks for ZFS mirror backup pool (e.g., sda,sdb)
#   --yes                       Skip confirmation prompt (for automation)
#   --zfs-encryption-keyfile    Enable ZFS native encryption on the main pool, reading a 32-byte raw key
#                               from the given path. The keyfile is generated on first run if missing.
#                               Operators MUST back this file up off-host — loss = unrecoverable pool.
#                               See docs/SECURITY-ENCRYPTION-AT-REST.md.
#   If no unused disks are found, falls back to file-backed 50GB pool.
#
# After running, reboot the machine if the NVIDIA driver was freshly installed.
# Then run: nvidia-smi  to verify GPU access.
#
# Tested on: Ubuntu 24.04 LTS (Noble)
#

set -euo pipefail

# ============================================================
# Pinned versions — update these when upgrading
# ============================================================
NVIDIA_DRIVER_VERSION="570"          # apt: nvidia-driver-570
INCUS_VERSION=""                      # leave empty for latest stable (6.x)
NVIDIA_CTK_VERSION=""                 # leave empty for latest stable

# ============================================================
# Options
# ============================================================
SKIP_REBOOT=false
AUTO_YES=false
DATA_DISK=""
BACKUP_DISKS=""
ZFS_ENCRYPTION_KEYFILE=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --skip-reboot) SKIP_REBOOT=true; shift ;;
        --yes|-y) AUTO_YES=true; shift ;;
        --data-disk) DATA_DISK="$2"; shift 2 ;;
        --backup-disks) BACKUP_DISKS="$2"; shift 2 ;;
        --zfs-encryption-keyfile) ZFS_ENCRYPTION_KEYFILE="$2"; shift 2 ;;
        --help|-h)
            echo "Usage: sudo $0 [--skip-reboot] [--yes] [--data-disk DISK] [--backup-disks DISK1,DISK2] [--zfs-encryption-keyfile PATH]"
            exit 0
            ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

# ============================================================
# Disk auto-detection
# ============================================================
# Returns unused whole disks (no partitions, not mounted, not in a zpool)
detect_unused_disks() {
    local nvme_disks=()
    local rotational_disks=()
    local ssd_disks=()

    while IFS= read -r disk; do
        [ -z "$disk" ] && continue
        local devname="/dev/$disk"

        # Skip if disk has partitions
        if lsblk -n -o TYPE "$devname" 2>/dev/null | grep -q part; then
            continue
        fi

        # Skip if any part of it is mounted
        if lsblk -n -o MOUNTPOINTS "$devname" 2>/dev/null | grep -q '/'; then
            continue
        fi

        # Skip if already in a zpool
        if zpool status 2>/dev/null | grep -q "$disk"; then
            continue
        fi

        # Classify by type
        if [[ "$disk" == nvme* ]]; then
            nvme_disks+=("$disk")
        elif [ -f "/sys/block/$disk/queue/rotational" ] && [ "$(cat "/sys/block/$disk/queue/rotational")" = "1" ]; then
            rotational_disks+=("$disk")
        else
            ssd_disks+=("$disk")
        fi
    done < <(lsblk -d -n -o NAME,TYPE 2>/dev/null | awk '$2=="disk"{print $1}')

    # Export results via global arrays
    DETECTED_NVME_COUNT=${#nvme_disks[@]}
    DETECTED_ROTATIONAL_COUNT=${#rotational_disks[@]}
    DETECTED_SSD_COUNT=${#ssd_disks[@]}
    DETECTED_NVME_LIST="${nvme_disks[*]:-}"
    DETECTED_ROTATIONAL_LIST="${rotational_disks[*]:-}"
    DETECTED_SSD_LIST="${ssd_disks[*]:-}"
    # First items for auto-selection
    DETECTED_NVME_FIRST="${nvme_disks[0]:-}"
    DETECTED_SSD_FIRST="${ssd_disks[0]:-}"
    DETECTED_ROT_FIRST="${rotational_disks[0]:-}"
    DETECTED_ROT_SECOND="${rotational_disks[1]:-}"
}

if [ -z "$DATA_DISK" ] || [ -z "$BACKUP_DISKS" ]; then
    detect_unused_disks

    # Auto-select data disk: prefer NVMe, then SSD
    if [ -z "$DATA_DISK" ]; then
        if [ -n "$DETECTED_NVME_FIRST" ]; then
            DATA_DISK="$DETECTED_NVME_FIRST"
        elif [ -n "$DETECTED_SSD_FIRST" ]; then
            DATA_DISK="$DETECTED_SSD_FIRST"
        fi
    fi

    # Auto-select backup disks: need at least 2 rotational disks for mirror
    if [ -z "$BACKUP_DISKS" ]; then
        if [ -n "$DETECTED_ROT_FIRST" ] && [ -n "$DETECTED_ROT_SECOND" ]; then
            BACKUP_DISKS="${DETECTED_ROT_FIRST},${DETECTED_ROT_SECOND}"
        fi
    fi

    # Show detection results and confirm
    echo "================================================"
    echo "Disk Auto-Detection Results"
    echo "================================================"
    if [ "$DETECTED_NVME_COUNT" -gt 0 ]; then
        echo "  NVMe (unused):       $DETECTED_NVME_LIST"
    fi
    if [ "$DETECTED_SSD_COUNT" -gt 0 ]; then
        echo "  SSD (unused):        $DETECTED_SSD_LIST"
    fi
    if [ "$DETECTED_ROTATIONAL_COUNT" -gt 0 ]; then
        echo "  HDD (unused):        $DETECTED_ROTATIONAL_LIST"
    fi
    echo ""
    echo "  Planned layout:"
    if [ -n "$DATA_DISK" ]; then
        _size=$(lsblk -d -n -o SIZE "/dev/$DATA_DISK" 2>/dev/null | tr -d ' ')
        echo "    Data pool:    /dev/$DATA_DISK ($_size) — ZFS for containers"
    else
        echo "    Data pool:    file-backed (50GB fallback)"
    fi
    if [ -n "$BACKUP_DISKS" ]; then
        IFS=',' read -ra _BD <<< "$BACKUP_DISKS"
        _size1=$(lsblk -d -n -o SIZE "/dev/${_BD[0]}" 2>/dev/null | tr -d ' ')
        echo "    Backup pool:  /dev/${_BD[0]} + /dev/${_BD[1]} ($_size1 each) — ZFS mirror"
    else
        echo "    Backup pool:  (none — no matching HDD pair found)"
    fi
    echo ""

    if [ "$AUTO_YES" = false ]; then
        read -rp "  Proceed with this layout? [y/N] " confirm
        if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
            echo "Aborted. Use --data-disk and --backup-disks to specify manually."
            exit 0
        fi
    fi
fi

# ============================================================
# Pre-flight checks
# ============================================================
if [ "$(id -u)" -ne 0 ]; then
    echo "ERROR: This script must be run as root (sudo)."
    exit 1
fi

if ! grep -q 'Ubuntu' /etc/os-release 2>/dev/null; then
    echo "WARNING: This script is tested on Ubuntu 24.04. Proceed at your own risk."
fi

echo "================================================"
echo "Containarium GPU Host Setup"
echo "================================================"
echo "  NVIDIA driver:   $NVIDIA_DRIVER_VERSION"
echo "  Incus:           ${INCUS_VERSION:-latest stable}"
echo "  NVIDIA CTK:      ${NVIDIA_CTK_VERSION:-latest stable}"
echo "  Data disk:       ${DATA_DISK:-(file-backed 50GB)}"
echo "  Backup disks:    ${BACKUP_DISKS:-(none)}"
echo ""

# ============================================================
# 1. System update & essentials
# ============================================================
echo "==> [1/8] Updating system packages..."

# Disable apt auto-upgrade timers — manual patching only.
# GPU peers run long-lived ML workloads; surprise package upgrades that
# restart docker / nvidia services would kill running training jobs.
# Re-enable with: systemctl enable --now apt-daily.timer apt-daily-upgrade.timer
systemctl disable --now apt-daily.timer apt-daily-upgrade.timer 2>/dev/null || true

apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y \
    curl wget git vim htop jq net-tools bridge-utils \
    ca-certificates gnupg lsb-release software-properties-common

# ============================================================
# 2. NVIDIA Driver
# ============================================================
echo "==> [2/8] Installing NVIDIA driver ${NVIDIA_DRIVER_VERSION}..."

NEED_REBOOT=false

if nvidia-smi &>/dev/null; then
    CURRENT_DRIVER=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader | head -1)
    echo "  NVIDIA driver already installed: $CURRENT_DRIVER"
else
    DEBIAN_FRONTEND=noninteractive apt-get install -y \
        nvidia-driver-${NVIDIA_DRIVER_VERSION}

    echo "  NVIDIA driver ${NVIDIA_DRIVER_VERSION} installed (reboot required)"
    NEED_REBOOT=true
fi

# ============================================================
# 3. NVIDIA Container Toolkit
# ============================================================
echo "==> [3/8] Installing NVIDIA Container Toolkit..."

if ! command -v nvidia-ctk &>/dev/null; then
    curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
        | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg

    curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
        | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
        | tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

    apt-get update

    if [ -n "$NVIDIA_CTK_VERSION" ]; then
        DEBIAN_FRONTEND=noninteractive apt-get install -y nvidia-container-toolkit="$NVIDIA_CTK_VERSION"
    else
        DEBIAN_FRONTEND=noninteractive apt-get install -y nvidia-container-toolkit
    fi
    echo "  NVIDIA Container Toolkit installed"
else
    echo "  NVIDIA Container Toolkit already installed: $(nvidia-ctk --version 2>/dev/null || echo 'unknown')"
fi

# ============================================================
# 4. Incus (from Zabbly)
# ============================================================
echo "==> [4/8] Installing Incus from Zabbly repository..."

# Remove Ubuntu's default Incus packages (6.0.0) if present — they conflict with Zabbly
if dpkg -l incus-tools 2>/dev/null | grep -q '^ii.*6\.0'; then
    echo "  Removing Ubuntu default incus packages (6.0.x) to avoid conflicts..."
    DEBIAN_FRONTEND=noninteractive apt-get remove -y incus incus-tools incus-client incus-base 2>/dev/null || true
fi

if [ ! -f /etc/apt/sources.list.d/zabbly-incus-stable.list ]; then
    curl -fsSL https://pkgs.zabbly.com/key.asc \
        | gpg --dearmor -o /usr/share/keyrings/zabbly-incus.gpg

    CODENAME=$(lsb_release -cs)
    echo "deb [signed-by=/usr/share/keyrings/zabbly-incus.gpg] https://pkgs.zabbly.com/incus/stable ${CODENAME} main" \
        | tee /etc/apt/sources.list.d/zabbly-incus-stable.list

    # Pin Zabbly repo higher than Ubuntu's default to avoid version conflicts
    cat > /etc/apt/preferences.d/zabbly-incus <<PINEOF
Package: incus* lxc* lxd*
Pin: origin pkgs.zabbly.com
Pin-Priority: 1001
PINEOF

    apt-get update
fi

if ! incus --version 2>/dev/null | grep -qE '^6\.(19|[2-9][0-9])'; then
    # Zabbly's incus package includes tools/client — do NOT install
    # Ubuntu's separate incus-tools/incus-client packages (they conflict)
    if [ -n "$INCUS_VERSION" ]; then
        DEBIAN_FRONTEND=noninteractive apt-get install -y "incus=$INCUS_VERSION"
    else
        DEBIAN_FRONTEND=noninteractive apt-get install -y incus
    fi
    echo "  Incus $(incus --version) installed"
else
    echo "  Incus already installed: $(incus --version)"
fi

# ============================================================
# 5. ZFS + Incus initialization
# ============================================================
echo "==> [5/8] Setting up ZFS and initializing Incus..."

if ! command -v zpool &>/dev/null; then
    DEBIAN_FRONTEND=noninteractive apt-get install -y zfsutils-linux
    modprobe zfs
    echo "  ZFS installed"
fi

if [ ! -f /var/lib/incus/.initialized ]; then
    # ZFS native encryption is opt-in via --zfs-encryption-keyfile.
    # The keyfile lives on the boot disk (separate from the data
    # disk holding the encrypted pool). Loss of the keyfile makes
    # the pool unrecoverable — operators MUST back it up off-host.
    # See docs/SECURITY-ENCRYPTION-AT-REST.md.
    ENCRYPT_OPTS=()
    if [ -n "$ZFS_ENCRYPTION_KEYFILE" ]; then
        mkdir -p "$(dirname "$ZFS_ENCRYPTION_KEYFILE")"
        if [ ! -f "$ZFS_ENCRYPTION_KEYFILE" ]; then
            echo "  Generating ZFS encryption keyfile at $ZFS_ENCRYPTION_KEYFILE"
            dd if=/dev/urandom of="$ZFS_ENCRYPTION_KEYFILE" bs=32 count=1 status=none
            chmod 0400 "$ZFS_ENCRYPTION_KEYFILE"
            echo "  WARNING: back up $ZFS_ENCRYPTION_KEYFILE off-host. Loss = unrecoverable pool."
        else
            echo "  Reusing existing ZFS encryption keyfile at $ZFS_ENCRYPTION_KEYFILE"
        fi
        ENCRYPT_OPTS=(-O encryption=on -O keyformat=raw -O keylocation="file://$ZFS_ENCRYPTION_KEYFILE")
    fi

    # --- Main storage pool (incus-local) ---
    if ! zpool list incus-local &>/dev/null; then
        if [ -n "$DATA_DISK" ]; then
            # Use dedicated NVMe disk for container storage
            DISK_PATH="/dev/${DATA_DISK}"
            if [ ! -b "$DISK_PATH" ]; then
                echo "ERROR: Data disk $DISK_PATH not found"
                exit 1
            fi
            # Safety check: refuse if disk has partitions (likely in use)
            if lsblk -n -o TYPE "$DISK_PATH" | grep -q part; then
                echo "ERROR: $DISK_PATH has existing partitions. Wipe it first if you're sure."
                exit 1
            fi
            zpool create \
                -o ashift=12 \
                -O compression=lz4 \
                -O atime=off \
                -O xattr=sa \
                -O recordsize=128k \
                "${ENCRYPT_OPTS[@]}" \
                -m /var/lib/incus/storage \
                incus-local "$DISK_PATH"
            if [ ${#ENCRYPT_OPTS[@]} -gt 0 ]; then
                echo "  ZFS pool created on $DISK_PATH with native encryption ($(lsblk -n -o SIZE "$DISK_PATH" | head -1))"
            else
                echo "  ZFS pool created on $DISK_PATH ($(lsblk -n -o SIZE "$DISK_PATH" | head -1))"
            fi
        else
            # Fallback: file-backed pool
            mkdir -p /var/lib/incus/disks
            truncate -s 50G /var/lib/incus/disks/incus.img
            zpool create \
                -o ashift=12 \
                -O compression=lz4 \
                -O atime=off \
                -O xattr=sa \
                "${ENCRYPT_OPTS[@]}" \
                -m /var/lib/incus/storage \
                incus-local /var/lib/incus/disks/incus.img
            if [ ${#ENCRYPT_OPTS[@]} -gt 0 ]; then
                echo "  ZFS pool created (file-backed, 50GB, encrypted)"
            else
                echo "  ZFS pool created (file-backed, 50GB)"
            fi
        fi
        zfs create incus-local/containers
    fi

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
    echo "  Incus initialized with ZFS"
else
    echo "  Incus already initialized"
fi

# Use ZFS refquota instead of quota for container disk limits.
# refquota counts ONLY live data; quota counts data + snapshots.
# With quota, daily backup snapshots eat into the user's visible disk
# (e.g. user asks for 500GB, sees 150GB after a heavy delete because the
# 350GB they freed is still pinned by yesterday's snapshot). refquota
# decouples user-facing capacity from snapshot retention. Idempotent.
