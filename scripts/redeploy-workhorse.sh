#!/usr/bin/env bash
# redeploy-workhorse.sh — safe, reversible upgrades for the Containarium
# workhorse OSS daemon (the cloud-tenants pool host).
#
# WHY THIS EXISTS
# ---------------
# The workhorse daemon historically ran as a *bare, hand-launched* process
# (`containarium daemon … &`, parented to init, no auto-restart, env only in
# that process). Upgrading meant: back up the binary, swap it, kill the
# process, and relaunch it by hand with the exact flags AND environment. A
# single wrong flag / missing KMS env / a failed eBPF attach takes the host
# that runs EVERY tenant box offline, with nothing to auto-restart it.
#
# This script turns that into a systemd-managed, health-gated, auto-rolling
# operation. Run order on a host the first time:
#
#   sudo ./redeploy-workhorse.sh capture     # adopt the live invocation+env → unit (no disruption)
#   sudo ./redeploy-workhorse.sh diff        # review the generated unit + env keys
#   sudo ./redeploy-workhorse.sh cutover      # ONE restart: bare process → systemd (auto-rollback)
#
# Thereafter, an upgrade is just:
#
#   sudo ./redeploy-workhorse.sh deploy v0.26.3   # download, verify, swap, restart, health-gate, auto-rollback
#   sudo ./redeploy-workhorse.sh rollback         # undo to the previous binary
#   sudo ./redeploy-workhorse.sh status           # version + unit + eBPF + listener
#
# It is deliberately conservative: every state-changing step has a health gate
# and restores the prior binary (and, on cutover failure, the prior bare
# process) if the daemon doesn't come back healthy.
#
# CONCURRENCY (cloud postmortem, 2026-09-22): a local shell timeout killed an
# operator's `deploy` invocation, but the REMOTE process kept running
# unattended (SSH without a pty does not guarantee a remote command dies with
# the local client). A second `deploy` was then launched, racing the still-live
# first one on the same binary + unit: a torn write, a failed `exec`, and a
# ~16 minute outage. This script now refuses a second concurrent invocation
# outright (see acquire_lock) instead of racing.
#
# SCOPE: this script swaps and supervises the daemon binary and unit only; it
# does not change the daemon's persisted configuration (`daemon_config`,
# including `base_domain`). For bringing up a BYOC backend and the post-upgrade
# checks, see docs/BYOC-BACKEND-BRINGUP.md.
set -euo pipefail

# ---- config (override via env) ---------------------------------------------
# Resolve through any symlink so every downstream decision (process-name
# detection, the release asset to download, what gets backed up/installed)
# is based on the binary that is ACTUALLY invoked, not a hardcoded guess.
# `install` (containarium.service's ExecStart, the Makefile's `make install`)
# lays down both the CLI-named compat symlink and containariumd as
# byte-identical/linked files (#1782), and per the containarium ->
# containariumd rename (#1781) the real install target is ALWAYS
# containariumd — the shorter name is legitimate only as that compat
# symlink. BIN_RAW is what got configured/exec'd (before resolving); BIN is
# the real file, resolved through any symlink, and is what every FILE
# operation (backup, install, current_version) acts on — content is shared
# either way, so operating on the real file is always correct there.
#
# PROCESS matching is a different story: Linux's `comm` (and argv[0] as
# read from /proc/<pid>/cmdline) reflects the LITERAL path a process was
# exec'd with, not its resolved realpath. A unit whose ExecStart names the
# compat symlink shows up in `ps` as "containarium" even though it's byte-
# identical to containariumd — readlink-ing BIN before matching processes
# fixes hosts that exec containariumd directly (2026-09-22's asia outage)
# but silently BREAKS hosts that exec the symlink instead, which is exactly
# as broken as the single hardcoded default this replaced. daemon_pid()
# therefore matches EITHER name rather than picking one.
BIN_RAW="${CTN_BIN:-/usr/local/bin/containariumd}"
BIN="$(readlink -f "$BIN_RAW" 2>/dev/null || echo "$BIN_RAW")"
# Was the unit pinned by the operator? Recorded BEFORE defaulting, because an
# explicit CTN_UNIT is a deliberate assertion we must not silently override —
# whereas the default is only a guess, and a wrong guess is what made this
# script capable of a false-success deploy (#1352).
CTN_UNIT_EXPLICIT=0
[ -n "${CTN_UNIT:-}" ] && CTN_UNIT_EXPLICIT=1
# Default to the unit the fleet ACTUALLY runs. Every workhorse (asia-east1 and
# us-west1, verified 2026-08-27) serves from containarium.service, installed by
# the host startup-script; containarium-daemon.service was this script's own
# invention and is inactive/disabled everywhere. Defaulting to the latter meant
# a deploy swapped the shared binary and then restarted a dead unit -- and, on
# a host where that unit file exists, could start a SECOND daemon fighting the
# live one for the HTTP/gRPC ports (#1352).
#
# deploy/redeploy-host.sh already forced CTN_UNIT=containarium.service to work
# around this; that override is now the default here, so the wrapper and the
# script agree instead of one compensating for the other.
UNIT_NAME="${CTN_UNIT:-containarium.service}"
UNIT_PATH="/etc/systemd/system/${UNIT_NAME}"
ENV_FILE="${CTN_ENV_FILE:-/etc/containarium/daemon.env}"
# The subcommand this host's daemon runs — how health detection recognises the
# process. Workhorses run `containarium daemon` (default); a sentinel runs
# `containarium sentinel`. Set CTN_SUBCMD=sentinel to roll a sentinel, or its
# health gate never finds the process and every deploy false-fails + rolls back.
SUBCMD="${CTN_SUBCMD:-daemon}"
HTTP_PORT="${CTN_HTTP_PORT:-8080}"
GH_REPO="${CTN_GH_REPO:-FootprintAI/Containarium}"
# Derived from BIN's (resolved) basename, so a host that execs containariumd
# downloads containariumd-linux-amd64 by default, matching the fleet's
# self-update convention (#1779) without needing a manual override. The two
# assets are byte-identical (#1782) so this is purely naming consistency, not
# a functional requirement.
RELEASE_ASSET="${CTN_ASSET:-$(basename "$BIN")-linux-amd64}"
HEALTH_TIMEOUT="${CTN_HEALTH_TIMEOUT:-60}"   # seconds to wait for a healthy daemon
LOCK_FILE="${CTN_LOCK_FILE:-/run/lock/containarium-redeploy-workhorse.lock}"

log() { printf '== %s\n' "$*" >&2; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
need_root() { [ "$(id -u)" = 0 ] || die "run as root (sudo)"; }

# Refuse a second concurrent invocation instead of racing it. Holds an flock
# on LOCK_FILE for the lifetime of this process (fd 9 closes, and the lock
# releases, on any exit — normal or `die`). Non-blocking: a second operator
# (or an orphaned first attempt that outlived its own caller) gets a clear
# refusal immediately, not a hang.
acquire_lock() {
  exec 9>"$LOCK_FILE" || die "cannot open lock file ${LOCK_FILE}"
  if ! flock -n 9; then
    die "another redeploy-workhorse.sh is already running against this host
  (lock: ${LOCK_FILE}). Refusing to run concurrently — this exact race (an
  orphaned first attempt + a second one racing it) caused a ~16 minute
  production outage on 2026-09-22. If you are certain no other invocation is
  actually running (e.g. the lock file is stale from a killed process), check
  \`fuser ${LOCK_FILE}\` before removing it by hand."
  fi
}

# Find the running daemon PID whether it's the bare process or systemd-managed.
# Anchor the match to the START of the cmdline ("<bin> daemon …") so it only
# ever matches the real daemon — NOT a bash wrapper, the containarium-runner-
# loop, this script, or the ssh session that's running it (all of which start
# with /bin/bash, not the binary path).
#
# Matches EITHER "containarium" or "containariumd" as the process name (see
# the BIN/BIN_RAW comment above for why): `pgrep -x` treats its argument as
# an extended regex requiring a full-string match, so 'containariumd?'
# matches exactly those two names and nothing else (never e.g.
# containarium-shell or containarium-runner-loop).
daemon_pid() {
  local p argv1
  for p in $(pgrep -x 'containariumd?' 2>/dev/null); do
    argv1="$(tr '\0' '\n' < "/proc/$p/cmdline" 2>/dev/null | sed -n '2p')"
    if [ "$argv1" = "$SUBCMD" ]; then echo "$p"; return 0; fi
  done
  # Fallback: anchored full-cmdline match against either the resolved real
  # path or the as-configured (possibly-symlink) path — whichever one this
  # host's unit actually execs.
  pgrep -f "^(${BIN}|${BIN_RAW}) ${SUBCMD}( |\$)" 2>/dev/null | head -1 || true
}

current_version() {
  "$BIN" version 2>/dev/null | grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo "unknown"
}

# Which systemd unit actually owns a PID? Read from its cgroup, which is the
# only authority — a unit NAME proves nothing about what is running.
#
# cgroup v2 gives a single line "0::/system.slice/containarium.service"; v1
# gives several, of which the systemd hierarchy is the one we want. Returns
# empty for a bare (non-systemd) process, which is a legitimate state here:
# `capture`/`cutover` exist precisely to adopt one.
unit_of_pid() {
  local pid="${1:-}" cg
  [ -n "$pid" ] || return 1
  cg="$(awk -F: '$2=="" || $2=="name=systemd" {print $3}' "/proc/$pid/cgroup" 2>/dev/null | head -1)"
  [ -n "$cg" ] || return 1
  # A non-service cgroup (a .scope, user.slice, a bare process) yields no
  # match. Return non-zero rather than an empty string with exit 0, so callers
  # can branch on either.
  local u
  u="$(printf '%s\n' "$cg" | grep -oE '[^/]+\.service' | tail -1)"
  [ -n "$u" ] || return 1
  printf '%s\n' "$u"
}

# Point UNIT_NAME at the unit that actually owns the live daemon.
#
# WHY (#1352): BIN is shared across units, so a deploy swaps the binary the
# LIVE daemon runs from, then restarts whatever UNIT_NAME happens to name. If
# that is not the owning unit, the live daemon is never restarted (and a
# disabled-but-present unit may even start a SECOND daemon that collides on
# the HTTP port). health_gate then matches the live process by cmdline shape
# and passes — so the script reports "deploy OK" having upgraded nothing.
# Observed on the asia-east1 primary: default containarium-daemon.service was
# inactive while containarium.service was serving.
resolve_unit() {
  local pid owner
  pid="$(daemon_pid)"
  if [ -z "$pid" ]; then
    log "no live '${SUBCMD}' process — proceeding with unit ${UNIT_NAME}"
    return 0
  fi
  owner="$(unit_of_pid "$pid" || true)"
  if [ -z "$owner" ]; then
    log "live daemon pid=${pid} is a bare process (no systemd unit) — proceeding with ${UNIT_NAME}"
    return 0
  fi
  if [ "$owner" = "$UNIT_NAME" ]; then
    return 0
  fi
  if [ "$CTN_UNIT_EXPLICIT" = 1 ]; then
    die "CTN_UNIT=${UNIT_NAME} but the live daemon (pid=${pid}) is owned by ${owner}.
  Refusing: acting on ${UNIT_NAME} would swap ${BIN} under ${owner} without restarting it.
  Re-run with CTN_UNIT=${owner}, or stop ${owner} first if you really mean ${UNIT_NAME}."
  fi
  log "NOTE: live daemon pid=${pid} is owned by ${owner}, not the default ${UNIT_NAME}"
  log "      retargeting this run at ${owner} (pass CTN_UNIT to pin it explicitly)"
  UNIT_NAME="$owner"
  UNIT_PATH="/etc/systemd/system/${UNIT_NAME}"
}

# --- health gate: the single source of "is the daemon OK after a change?" ---
# Hard gate (rollback if any fail):
#   1. process for ${BIN} daemon is alive
#   2. it's LISTENING on ${HTTP_PORT}
#   3. no panic / fatal in the unit's recent journal
# Soft check (warn only): the per-veth eBPF program is attached.
health_gate() {
  local deadline=$(( $(date +%s) + HEALTH_TIMEOUT ))
  local ok_proc=0 ok_port=0
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if [ -n "$(daemon_pid)" ]; then ok_proc=1; else ok_proc=0; fi
    if ss -ltn 2>/dev/null | grep -q ":${HTTP_PORT} "; then ok_port=1; else ok_port=0; fi
    [ "$ok_proc" = 1 ] && [ "$ok_port" = 1 ] && break
    sleep 2
  done
