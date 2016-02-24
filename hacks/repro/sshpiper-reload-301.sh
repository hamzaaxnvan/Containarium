#!/usr/bin/env bash
#
# sshpiper-reload-301.sh — minimal repro/validation harness for issue #301
# ("sshpiper reload on container create/delete drops all live SSH sessions").
#
# WHAT THIS ANSWERS
#   The sentinel rewrites /etc/sshpiper/config.yaml on every keysync that
#   changes the routing table (container create/delete) and then calls
#   `systemctl restart sshpiper` (internal/sentinel/keysync.go:RestartSSHPiper),
#   which tears down EVERY live SSH session. The fix hinges on one runtime
#   question that can't be answered from source:
#
#     Does the sshpiperd YAML plugin pick up config.yaml changes for NEW
#     connections WITHOUT a restart, while leaving EXISTING sessions alive?
#
#   This script stands up a hermetic sshpiper + a throwaway upstream sshd,
#   holds a live session, rewrites the config to add a route (simulating a
#   container create), and checks:
#     A. the held session survives a config rewrite with NO restart, and
#        a brand-new user can connect against the rewritten config; →  Option A
#        (just delete the RestartSSHPiper() calls).
#     B. if new connections need a nudge, whether SIGHUP / reload picks up
#        the new route WITHOUT dropping the held session.               →  Option B
#   It also runs a CONTROL that does a real restart and confirms the held
#   session DROPS — so a "survived" result can't be a false negative.
#
# FIDELITY
#   The YAML written here matches keysync.go's format exactly (version "1.0",
#   pipes: from.username/authorized_keys, to.host/username/ignore_hostkey/
#   private_key). To test the EXACT binary production runs, point the harness
#   at it:  SSHPIPERD_BIN=/usr/local/bin/sshpiperd  (and, if its CLI differs,
#   override the launch line via SSHPIPERD_LAUNCH — see below). Get production's
#   invocation from `systemctl cat sshpiper` on a real sentinel.
#
# REQUIREMENTS
#   - Linux, run as root (creates a throwaway unix user, runs sshd).
#   - A throwaway VM — this creates a user and runs daemons. Do NOT run on a host
#     you care about. Cleanup removes everything it created.
#   - sshpiperd on PATH or at $SSHPIPERD_BIN (or set SSHPIPERD_VERSION to fetch).
#   - openssh-server (sshd), openssh-client (ssh), ssh-keygen.
#
# USAGE
#   sudo ./sshpiper-reload-301.sh
#   sudo SSHPIPERD_BIN=/usr/local/bin/sshpiperd ./sshpiper-reload-301.sh
#   sudo SSHPIPERD_VERSION=v1.4.0 ./sshpiper-reload-301.sh     # fetch if absent
#
set -euo pipefail

# ---- config (override via env) ---------------------------------------------
WORK="${WORK:-/tmp/sp301}"
PIPER_PORT="${PIPER_PORT:-2222}"      # downstream: clients connect here
UPSTREAM_PORT="${UPSTREAM_PORT:-2200}" # upstream throwaway sshd
UPSTREAM_USER="${UPSTREAM_USER:-sp_up}"
SSHPIPERD_BIN="${SSHPIPERD_BIN:-$(command -v sshpiperd || true)}"
SSHPIPERD_VERSION="${SSHPIPERD_VERSION:-v1.4.0}"
CONFIG="$WORK/config.yaml"
USERS_DIR="$WORK/users"            # mirrors sshpiperUsersDir layout

RED=$'\033[0;31m'; GRN=$'\033[0;32m'; YEL=$'\033[1;33m'; BLU=$'\033[0;34m'; NC=$'\033[0m'
section() { echo; echo "${BLU}== $* ==${NC}"; }
pass()    { echo "${GRN}PASS${NC} $*"; }
fail()    { echo "${RED}FAIL${NC} $*"; }
info()    { echo "${YEL}····${NC} $*"; }

PIPER_PID=""; SSHD_PID=""
cleanup() {
  set +e
  [ -n "$PIPER_PID" ] && kill "$PIPER_PID" 2>/dev/null
  [ -n "$SSHD_PID" ]  && kill "$SSHD_PID"  2>/dev/null
  ssh -S "$WORK/cm-alice" -O exit alice@127.0.0.1 2>/dev/null
  userdel -r "$UPSTREAM_USER" 2>/dev/null
  rm -rf "$WORK"
}
trap cleanup EXIT

# ---- preflight -------------------------------------------------------------
section "preflight"
[ "$(id -u)" -eq 0 ] || { fail "must run as root (sudo)"; exit 2; }
[ "$(uname -s)" = "Linux" ] || { fail "Linux only (sshpiperd/sshd)"; exit 2; }
for b in ssh ssh-keygen sshd; do command -v "$b" >/dev/null || { fail "missing $b (install openssh-server + openssh-client)"; exit 2; }; done
