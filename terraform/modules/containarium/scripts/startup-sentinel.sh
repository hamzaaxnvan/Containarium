#!/bin/bash
set -euo pipefail

# Sentinel VM startup script — minimal setup
# Only installs the containarium binary and runs it in sentinel mode.
# No Incus, no Caddy, no ZFS — just networking and the binary.

exec 1> >(logger -s -t containarium-sentinel-startup) 2>&1
echo "=== Containarium Sentinel Startup ==="

# Template variables
ADMIN_USERS="${join(",", admin_users)}"
CONTAINARIUM_VERSION="${containarium_version}"
CONTAINARIUM_BINARY_URL="${containarium_binary_url}"
SPOT_VM_NAME="${spot_vm_name}"
ZONE="${zone}"
PROJECT_ID="${project_id}"

# reconcile_containarium_binary installs/updates /usr/local/bin/containariumd
# to the desired version (CONTAINARIUM_VERSION), then leaves the service
# install+restart below to pick it up. The sentinel serves THIS SAME binary to
# recovered workhorses on :8888 (internal/sentinel/binaryserver.go's
# defaultBinaryPath, #1779) and runs its own sentinel process from it, so it
# must honor the requested version — and the previous unconditional
# `curl -o <live binary>` was both non-version-aware and unsafe (a failed
# download under `set -e` aborted the whole boot, and it overwrote the running
# binary in place). See #385.
#
# A real pinned version that already matches is a no-op (faster boots, and the
# sentinel won't re-pull a same-version binary). "dev"/unset keep the prior
# always-pull-from-URL behavior so the dev iteration loop is unchanged. Download
# is to a temp path + atomic swap, and the whole thing is best-effort.
reconcile_containarium_binary() {
    desired="$CONTAINARIUM_VERSION"
    current=""
    if [ -x /usr/local/bin/containariumd ]; then
        current="$(/usr/local/bin/containariumd version 2>/dev/null || true)"
    fi

    # Skip only for a real pinned version that already matches. "dev" and unset
    # always re-pull (preserving the sentinel's prior every-boot behavior).
    if [ -n "$desired" ] && [ "$desired" != "dev" ] && printf '%s' "$current" | grep -qF "$desired"; then
        echo "containariumd already at desired version ($desired) — skipping download"
        return 0
    fi

    if [ -z "$CONTAINARIUM_BINARY_URL" ]; then
        if [ -x /usr/local/bin/containariumd ]; then
            echo "no containarium_binary_url; keeping existing binary ($current)"
        else
            echo "WARNING: no containarium binary source available"
        fi
        return 0
    fi

    echo "Downloading containariumd from $CONTAINARIUM_BINARY_URL"
    tmp=/usr/local/bin/.containariumd.new
    rm -f "$tmp"
    if curl -fsSL "$CONTAINARIUM_BINARY_URL" -o "$tmp"; then
        chmod +x "$tmp"
        mv -f "$tmp" /usr/local/bin/containariumd
        # Rollout Phase 1 (#1781): keep the old name working as a symlink so
        # any runbook/cron this inventory missed keeps working.
        ln -sf /usr/local/bin/containariumd /usr/local/bin/containarium
        echo "Containariumd version: $(/usr/local/bin/containariumd version 2>/dev/null || echo unknown)"
    else
        rm -f "$tmp"
        if [ -x /usr/local/bin/containariumd ]; then
            echo "WARNING: download failed; keeping existing binary ($current)"
        else
            echo "WARNING: download failed and no existing binary; sentinel will not start"
        fi
    fi
    return 0
}

# Trim unnecessary services that consume 150-200 MB on the sentinel (#770).
# snapd (~38 MB), multipathd (~27 MB), update-notifier/check-new-release (~90 MB
# transient) are irrelevant on a pure-forwarder VM. Removing them frees headroom
# so a reconnect storm or GC spike can't freeze the entire host.
systemctl stop snapd.service snapd.socket 2>/dev/null || true
systemctl disable snapd.service snapd.socket 2>/dev/null || true
apt-get purge -y -qq snapd 2>/dev/null || true
apt-get purge -y -qq update-notifier update-notifier-common 2>/dev/null || true
systemctl stop multipathd.service multipathd.socket 2>/dev/null || true
systemctl disable multipathd.service multipathd.socket 2>/dev/null || true
systemctl mask multipathd.service multipathd.socket 2>/dev/null || true

# Disable apt auto-upgrade timers — manual patching only.
# Auto-upgrades on a small sentinel VM caused OOM hangs when
# unattended-upgrades + packagekit + sshpiper ran concurrently.
# Re-enable with: systemctl enable --now apt-daily.timer apt-daily-upgrade.timer
systemctl disable --now apt-daily.timer apt-daily-upgrade.timer 2>/dev/null || true

# System update (minimal)
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl fail2ban > /dev/null

# Persist the admin sshd (port 2222) host key across restarts/redeploys
# (#1596). It was observed to regenerate on every containarium-sentinel
# (re)start on this image, which degrades StrictHostKeyChecking from a
# real MITM signal into routine "host key has changed" noise that trains
# operators to blindly ssh-keygen -R and move on — exactly the response a
# real attack needs. Generate our own key once, the same idempotent
# pattern already used below for sshpiper's own host_key (which has never
# shown this problem — same disk, so the persistence itself is fine; the
# bug is specifically in leaving this to the OS's own /etc/ssh/ssh_host_*
# lifecycle), and make sshd use ONLY this key.
mkdir -p /etc/containarium
if [ ! -f /etc/containarium/ssh_host_ed25519_key ]; then
    ssh-keygen -t ed25519 -f /etc/containarium/ssh_host_ed25519_key -N ""
    echo "admin sshd host key generated"
fi
chmod 600 /etc/containarium/ssh_host_ed25519_key

# Harden SSH — sshd listens on port 2222 ONLY (management/IAP access)
# Port 22 is now owned by sshpiper (SSH reverse proxy with fail-to-ban)
cat > /etc/ssh/sshd_config.d/containarium.conf <<EOF
