#!/usr/bin/env bash
#
# mount-watchdog — detect and recover containarium.service left permanently
# dead by a RequiresMountsFor dependency failure (#1317).
#
# containarium.service ships Wants=incus.service, not Requires=, specifically
# to avoid a hard dependency killing its start job with no retry (#1152). A
# host that mounts /var/lib/incus from external or encrypted storage has to
# reintroduce that dependency class via RequiresMountsFor=/var/lib/incus --
# otherwise the daemon can start against an empty, not-yet-mounted directory.
# RequiresMountsFor has NO retry semantics: a transient failure anywhere in
# the mount's own dependency chain (a LUKS systemd-cryptsetup@ unit, or the
# .device unit underneath it missing a udev event) leaves containarium.service
# inactive (dead) PERMANENTLY -- zero journal entries for that boot, no
# alert, no automatic recovery.
#
# See docs/NODE-VM-PROVISIONING.md's "Encrypted / external storage" section
# for the preventive fix (an ExecStartPost udev-kick drop-in on the
# cryptsetup unit, which avoids the failure in the first place). This
# watchdog is the second layer: it catches the failure if it happens anyway
# and recovers without an operator noticing first, the same way
# deploy/incus-watchdog covers a different silent-failure class for incusd.
#
# Detection: SERVICE is enabled (this host expects it running) but not
# active, AND MOUNTPOINT is not currently mounted. That combination is the
# exact #1317 signature -- a mount-dependency chain failure, not an ordinary
# daemon crash. An ordinary crash (or an exhausted Restart=on-failure) leaves
# the mount up, so this does not false-positive on that case.
#
# Recovery mirrors the incident's proven manual fix: re-announce every
# dm-mapper block device to udev (recovers a stuck .device unit waiting on a
# lost "add" event), clear the resulting failed states on the specific unit
# classes the incident named, then start the mount and the daemon.
#
# Config via env (systemd EnvironmentFile or the unit's Environment=):
#   MOUNT_WATCHDOG_INTERVAL    seconds between checks              (default 60)
#   MOUNT_WATCHDOG_MOUNTPOINT  path that must be mounted            (default /var/lib/incus)
#   MOUNT_WATCHDOG_SERVICE     unit that depends on it              (default containarium.service)
set -uo pipefail

INTERVAL="${MOUNT_WATCHDOG_INTERVAL:-60}"
MOUNTPOINT="${MOUNT_WATCHDOG_MOUNTPOINT:-/var/lib/incus}"
SERVICE="${MOUNT_WATCHDOG_SERVICE:-containarium.service}"

log() { logger -t mount-watchdog -- "$*" 2>/dev/null || true; echo "mount-watchdog: $*"; }

# wedged reports whether SERVICE is enabled, not currently active, and
# MOUNTPOINT is not mounted -- the #1317 signature.
wedged() {
	local enabled active
	enabled=$(systemctl is-enabled "${SERVICE}" 2>/dev/null || echo "")
