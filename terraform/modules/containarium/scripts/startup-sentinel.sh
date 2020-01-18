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
PasswordAuthentication no
PubkeyAuthentication yes
PermitRootLogin no
HostKey /etc/containarium/ssh_host_ed25519_key
EOF
# Drop the distro's default HostKey lines (same removal pattern as Port
# below) so sshd offers ONLY our persisted key, not whatever it or cloud-init
# regenerated under /etc/ssh/ this boot.
sed -i '/^HostKey /d' /etc/ssh/sshd_config
# Set sshd to port 2222 only — port 22 is reserved for sshpiper
sed -i '/^Port /d' /etc/ssh/sshd_config
echo "Port 2222" >> /etc/ssh/sshd_config
# Ubuntu 24.04 uses systemd socket activation — override ssh.socket too
mkdir -p /etc/systemd/system/ssh.socket.d
cat > /etc/systemd/system/ssh.socket.d/override.conf <<EOF
[Socket]
ListenStream=
ListenStream=0.0.0.0:2222
ListenStream=[::]:2222
EOF
systemctl daemon-reload
systemctl restart ssh.socket || true
systemctl restart ssh || systemctl restart sshd || true

# Create admin users
IFS=',' read -ra USERS <<< "$ADMIN_USERS"
for username in "$${USERS[@]}"; do
    username=$(echo "$username" | xargs)
    if [ -z "$username" ]; then continue; fi
    if ! id "$username" &>/dev/null; then
        # Create user without creating a new primary group (use existing or
        # fall back to "users") — a bare useradd tries to auto-create a
        # same-named primary group, which fails outright if a group of that
        # name already exists (e.g. "admin" collides with a pre-existing
        # system group shipped in the stock Ubuntu image; kafeido-infra#41).
        # This script runs under `set -euo pipefail`, so an unhandled
        # failure here previously aborted the ENTIRE sentinel bootstrap
        # (sshpiper, containarium, everything) over one bad username.
        useradd -m -s /bin/bash -N -G sudo "$username" || useradd -m -s /bin/bash -g users -G sudo "$username"
        echo "$username ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/"$username"
        chmod 440 /etc/sudoers.d/"$username"
        mkdir -p /home/"$username"/.ssh
        chmod 700 /home/"$username"/.ssh
        chown -R "$username":"$username" /home/"$username"/.ssh
    fi
done

# Install sshpiper — SSH reverse proxy with built-in fail-to-ban
# sshpiper sits on port 22, sees real client IPs, and bans after failed auths
echo "==> Installing sshpiper..."
SSHPIPER_VERSION="v1.6.1"
# sha256 from the v1.6.1 release checksums.txt; keep in step with
# .github/workflows/ssh-ban.yml (scripts/ci/check-sentinel-sshpiper-checksum.sh).
SSHPIPER_SHA256="95d423a70e843a7512a72fbb16c0fa5dc59217cf064cb3e277520fddffded1e1"
if [ ! -f /usr/local/bin/sshpiperd ]; then
    cd /tmp
    curl -fsSL "https://github.com/tg123/sshpiper/releases/download/$${SSHPIPER_VERSION}/sshpiperd_with_plugins_linux_x86_64.tar.gz" \
      -o sshpiper.tar.gz
    # A mismatch fails the boot step (set -e) before anything is extracted.
    echo "$${SSHPIPER_SHA256}  sshpiper.tar.gz" | sha256sum -c -
    tar xzf sshpiper.tar.gz
    mv sshpiperd /usr/local/bin/sshpiperd
    chmod +x /usr/local/bin/sshpiperd
    # Plugins are separate binaries (yaml, failtoban, etc.)
    cp plugins/* /usr/local/bin/
    chmod +x /usr/local/bin/yaml /usr/local/bin/failtoban
    rm -rf sshpiper.tar.gz plugins LICENSE README.md
    echo "sshpiper $${SSHPIPER_VERSION} installed"
else
    echo "sshpiper already installed"
fi

# Generate sshpiper host key + upstream key for authenticating to spot VM
mkdir -p /etc/sshpiper/users
if [ ! -f /etc/sshpiper/host_key ]; then
    ssh-keygen -t ed25519 -f /etc/sshpiper/host_key -N ""
    echo "sshpiper host key generated"
fi
if [ ! -f /etc/sshpiper/upstream_key ]; then
    ssh-keygen -t ed25519 -f /etc/sshpiper/upstream_key -N ""
    echo "sshpiper upstream key generated"
fi

# reconcile_sshpiper_service fetches the desired /etc/systemd/system/sshpiper.service
# content LIVE from the "sshpiper-service-unit" instance-metadata key (not an
# inline heredoc) and applies it only if it differs from what's on disk.
#
# Why: this whole startup-script only runs once, at first boot. A `terraform
# apply` that tunes the unit (e.g. failtoban's --max-failures/--ban-duration)
# updates the instance's metadata immediately, but an already-running
# sentinel never re-reads it — the change silently never reaches production
# until someone manually intervenes or the VM is recreated (issue #933: a
# sentinel was found running month-old, far more aggressive failtoban
# settings than what had shipped in this repo twice since). GCE metadata
# values ARE fetchable live at any time via the metadata server, so this
# function is installed as a periodic systemd timer (below) in addition to
# running here at boot — the SAME source of truth both times, no drift.
reconcile_ssh_upstream_control() {
    policy="$(curl -fsS -H 'Metadata-Flavor: Google' \
        'http://metadata.google.internal/computeMetadata/v1/instance/attributes/ssh-upstream-policy')" || return 1
    install -d -m 0700 /etc/containarium/ssh-upstream
    install -d -m 0755 /etc/systemd/system/containarium-sentinel.service.d
    dropin=/etc/systemd/system/containarium-sentinel.service.d/ssh-upstream.conf
    active=/etc/containarium/ssh-upstream/policy.json
    pending=/etc/containarium/ssh-upstream/policy.pending.json
    changed=false
    dropin_created=false
    if [ ! -f "$active" ]; then
        printf '%s' "$policy" > "$active"
        chmod 0600 "$active"
        changed=true
    elif [ "$(cat "$active")" != "$policy" ]; then
        # Keep the active policy/checkpoint pair valid even if an unrelated
        # restart happens before the operator's maintenance window.
        if [ ! -f "$pending" ] || [ "$(cat "$pending")" != "$policy" ]; then
            printf '%s' "$policy" > "$pending"
            chmod 0600 "$pending"
            changed=true
        fi
