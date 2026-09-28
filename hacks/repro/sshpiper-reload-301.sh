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
SSHD_BIN="$(command -v sshd)"

if [ -z "$SSHPIPERD_BIN" ]; then
  info "sshpiperd not found; fetching $SSHPIPERD_VERSION from github.com/tg123/sshpiper releases"
  arch="$(uname -m)"; case "$arch" in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; esac
  url="https://github.com/tg123/sshpiper/releases/download/${SSHPIPERD_VERSION}/sshpiperd_linux_${arch}"
  curl -fsSL "$url" -o /usr/local/bin/sshpiperd || { fail "download failed: $url (set SSHPIPERD_BIN to a local binary)"; exit 2; }
  chmod +x /usr/local/bin/sshpiperd
  SSHPIPERD_BIN=/usr/local/bin/sshpiperd
fi
info "sshpiperd binary: $SSHPIPERD_BIN"
info "sshpiperd version: $("$SSHPIPERD_BIN" --version 2>&1 | head -1 || echo '(unknown)')"
info "RECORD THIS VERSION — the answer is a property of this exact build."

# ---- setup -----------------------------------------------------------------
section "setup (hermetic sshpiper + throwaway upstream sshd)"
rm -rf "$WORK"; mkdir -p "$WORK" "$USERS_DIR/alice" "$USERS_DIR/bob"

# Keys: downstream client keys (alice/bob) + the sentinel's upstream key.
ssh-keygen -q -t ed25519 -N "" -f "$WORK/alice_id"   -C alice
ssh-keygen -q -t ed25519 -N "" -f "$WORK/bob_id"     -C bob
ssh-keygen -q -t ed25519 -N "" -f "$WORK/upstream"   -C sentinel-upstream   # == sshpiperUpstreamKey
ssh-keygen -q -t ed25519 -N "" -f "$WORK/piper_hostkey" -C piper-hostkey
ssh-keygen -q -t ed25519 -N "" -f "$WORK/sshd_hostkey"  -C sshd-hostkey

# Per-user authorized_keys (downstream auth) — mirrors sshpiperUsersDir.
cp "$WORK/alice_id.pub" "$USERS_DIR/alice/authorized_keys"
cp "$WORK/bob_id.pub"   "$USERS_DIR/bob/authorized_keys"
chmod 600 "$USERS_DIR"/*/authorized_keys

# Throwaway upstream account + its authorized_keys (accepts the sentinel key).
id "$UPSTREAM_USER" >/dev/null 2>&1 || useradd -m -s /bin/bash "$UPSTREAM_USER"
install -d -m 700 "$WORK/up_ssh"
cp "$WORK/upstream.pub" "$WORK/up_ssh/authorized_keys"
chmod 600 "$WORK/up_ssh/authorized_keys"
chown -R "$UPSTREAM_USER" "$WORK/up_ssh"

# Dedicated upstream sshd (does not touch the system sshd).
cat > "$WORK/sshd_config" <<EOF
Port $UPSTREAM_PORT
ListenAddress 127.0.0.1
HostKey $WORK/sshd_hostkey
PidFile $WORK/sshd.pid
AuthorizedKeysFile $WORK/up_ssh/authorized_keys
UsePAM no
PasswordAuthentication no
PubkeyAuthentication yes
StrictModes no
LogLevel ERROR
EOF
"$SSHD_BIN" -f "$WORK/sshd_config" -D & SSHD_PID=$!

# config.yaml — EXACT shape from keysync.go (one pipe: alice -> upstream).
write_config() {
  # $1 = "alice" or "alice bob" (space-separated downstream usernames)
  { echo 'version: "1.0"'; echo 'pipes:'
    for u in $1; do
      echo "  - from:"
      echo "      - username: \"$u\""
      echo "        authorized_keys:"
      echo "          - $USERS_DIR/$u/authorized_keys"
      echo "    to:"
      echo "      host: 127.0.0.1:$UPSTREAM_PORT"
      echo "      username: \"$UPSTREAM_USER\""
      echo "      ignore_hostkey: true"
      echo "      private_key: $WORK/upstream"
    done
  } > "$CONFIG"
}
write_config "alice"

# Launch sshpiperd (override the whole line via SSHPIPERD_LAUNCH if your build's
# CLI differs — paste production's args).
start_piper() {
  if [ -n "${SSHPIPERD_LAUNCH:-}" ]; then
    eval "$SSHPIPERD_LAUNCH" & PIPER_PID=$!
  else
    "$SSHPIPERD_BIN" -p "$PIPER_PORT" -i "$WORK/piper_hostkey" --log-level info \
      yaml --config "$CONFIG" >"$WORK/piper.log" 2>&1 & PIPER_PID=$!
  fi
}
start_piper
sleep 2
kill -0 "$PIPER_PID" 2>/dev/null || { fail "sshpiperd did not start — see $WORK/piper.log"; sed -n '1,40p' "$WORK/piper.log"; \
  info "If the CLI differs for your build, re-run with SSHPIPERD_LAUNCH set to production's invocation."; exit 3; }

SSH_OPTS=(-p "$PIPER_PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=8 -o LogLevel=ERROR)
conn() { ssh "${SSH_OPTS[@]}" -i "$1" "$2@127.0.0.1" "${3:-echo connected}" 2>/dev/null; }

# ---- baseline --------------------------------------------------------------
section "baseline"
if out=$(conn "$WORK/alice_id" alice "echo connected"); [ "$out" = connected ]; then
  pass "alice connects through sshpiper → upstream"
else
  fail "baseline connect failed — environment/CLI issue, not a #301 result"; sed -n '1,40p' "$WORK/piper.log"; exit 3
fi

# Hold a live session via ControlMaster; its TCP dies if sshpiperd restarts.
ssh "${SSH_OPTS[@]}" -i "$WORK/alice_id" -M -S "$WORK/cm-alice" -fN alice@127.0.0.1
held_alive() { ssh -S "$WORK/cm-alice" -O check alice@127.0.0.1 >/dev/null 2>&1; }
held_alive && pass "held alice session established" || { fail "could not hold session"; exit 3; }

# ---- Option A: config rewrite, NO restart ----------------------------------
section "Option A — rewrite config.yaml adding 'bob' (NO restart)"
write_config "alice bob"
info "config rewritten; sshpiperd NOT signalled"
sleep 2

A_held=fail; A_new=fail
held_alive && { A_held=pass; pass "held alice session SURVIVED the rewrite"; } || fail "held alice session dropped on plain rewrite (unexpected)"
if out=$(conn "$WORK/bob_id" bob "echo connected"); [ "$out" = connected ]; then
  A_new=pass; pass "new user 'bob' connects with NO restart → YAML plugin hot-reloads"
else
  fail "new user 'bob' canNOT connect without a restart → plugin does not hot-reload new pipes"
fi

# ---- Option B: SIGHUP / reload (only meaningful if A_new failed) -----------
B_held=skip; B_new=skip
if [ "$A_new" != pass ]; then
  section "Option B — SIGHUP sshpiperd, re-test"
  kill -HUP "$PIPER_PID" 2>/dev/null || info "SIGHUP not delivered"
  sleep 2
  if kill -0 "$PIPER_PID" 2>/dev/null && held_alive; then B_held=pass; pass "held session survived SIGHUP"; else B_held=fail; fail "SIGHUP dropped the held session (or killed sshpiperd)"; fi
  if out=$(conn "$WORK/bob_id" bob "echo connected"); [ "$out" = connected ]; then B_new=pass; pass "'bob' connects after SIGHUP"; else B_new=fail; fail "'bob' still cannot connect after SIGHUP"; fi
fi

# ---- CONTROL: real restart MUST drop the held session ----------------------
# Proves the harness can actually detect a drop (guards against false 'survived').
section "CONTROL — full restart (the current behavior) must DROP the held session"
held_alive && info "held session alive before restart"
kill "$PIPER_PID" 2>/dev/null; wait "$PIPER_PID" 2>/dev/null || true
start_piper; sleep 2
if held_alive; then
  fail "held session still 'alive' after a full restart — harness cannot detect drops; treat all results as INCONCLUSIVE"
  CONTROL=fail
else
  pass "held session dropped on full restart (as expected) — harness is sensitive"
  CONTROL=pass
fi

# ---- verdict ---------------------------------------------------------------
section "VERDICT"
echo "  sshpiperd: $("$SSHPIPERD_BIN" --version 2>&1 | head -1)"
echo "  control (restart drops session): $CONTROL"
echo "  A: held-survives-rewrite=$A_held  new-user-no-restart=$A_new"
echo "  B: held-survives-SIGHUP=$B_held   new-user-after-SIGHUP=$B_new"
echo
if [ "$CONTROL" != pass ]; then
  echo "${RED}INCONCLUSIVE${NC} — control failed; the harness couldn't detect a dropped session."
elif [ "$A_held" = pass ] && [ "$A_new" = pass ]; then
  echo "${GRN}OPTION A CONFIRMED${NC} — the YAML plugin hot-reloads. Fix = delete the"
  echo "  RestartSSHPiper() calls (keysync.go:325, manager.go:668, manager.go:1083);"
  echo "  just write config.yaml. Live sessions stop dropping."
elif [ "$B_held" = pass ] && [ "$B_new" = pass ]; then
  echo "${GRN}OPTION B CONFIRMED${NC} — SIGHUP picks up new routes WITHOUT dropping"
  echo "  sessions. Replace 'systemctl restart' with reload/SIGHUP in RestartSSHPiper()."
else
  echo "${YEL}NEITHER A NOR B${NC} — this sshpiperd build needs a restart to see new routes"
  echo "  and can't reload gracefully. Fall back to Option C (coalesce restarts) now and"
  echo "  plan Option E (custom gRPC upstream plugin) for a durable fix. See the #301 note."
fi
