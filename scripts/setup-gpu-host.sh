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
