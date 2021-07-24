#!/usr/bin/env bash
#
# Backend acceptance for eBPF virtual patching — Tier 1 L3/L4 deny rules (#660).
# Companion to VALIDATION-traffic-flows.md, but runnable: it drives the daemon's
# network-policy API + the container's egress to prove a virtual-patch deny rule
#   - beats the allow-list (deny-by-CIDR overrides a 0.0.0.0/0 allow),
#   - can be scoped to a port/proto,
#   - self-removes at its expiry,
#   - is cleared by `patch rm`,
# and that none of it regresses the existing allow-list path.
#
# The compiled BPF object is never committed (built on the backend / in CI), so
# this runbook is what exercises the new deny_cidr map + the deny-first branch in
# the kernel verifier at first load. The Go layers are unit-tested separately.
#
# It ADAPTS to whether enforcement is armed:
#   - CONTAINARIUM_NETWORK_POLICY_ENFORCE=1 on the daemon  → drops are asserted.
#   - unset (observe-only)                                 → drop asserts become
#     SKIPs; the audit/log path (action network_policy.virtual_patch) is still
#     verified. That is the safe default soak mode.
#
# Anonymise before pasting results anywhere public (CLAUDE.md): use <backend>,
# <tenant>, $DAEMON, $JWT — never real hostnames / IPs / tenant names.
#
# Usage:
#   DAEMON=http://127.0.0.1:8080 JWT=<admin-jwt> CONTAINER=<tenant>-container \
#     ./validate-virtual-patch.sh
#
# Optional env:
#   TENANT          tenant name (default: derived from CONTAINER by stripping
#                   the trailing "-container")
#   PROBE_IP        reachable test destination to block (default 1.1.1.1)
#   PROBE_PORT      TCP port for the port-scoped test (default 443)
#   TIMEOUT         per-probe timeout seconds (default 5)
#   RECONCILE_WAIT  seconds to wait for a reconcile to apply a policy change
#                   (default 15; the loop ticks every 10s)
#   EXPIRY_SECS     seconds-from-now for the expiry test rule (default 45)
#   JOURNAL_UNIT    systemd unit for the audit/log check (default containarium)
#   CTR             containarium CLI binary (default: containarium)
#   BUILD=1         (re)compile netpolicy.bpf.o to $BPF_OBJ before validating
#   BPF_OBJ         object path for BUILD=1 (default /etc/containarium/netpolicy.bpf.o)
#   SKIP_MAP_CHECK=1  bypass the "loaded object has deny_cidr" preflight
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DAEMON="${DAEMON:?set DAEMON to the daemon HTTP address, e.g. http://127.0.0.1:8080}"
JWT="${JWT:?set JWT to an admin token}"
CONTAINER="${CONTAINER:?set CONTAINER to a throwaway tenant container name}"
TENANT="${TENANT:-${CONTAINER%-container}}"
PROBE_IP="${PROBE_IP:-1.1.1.1}"
PROBE_PORT="${PROBE_PORT:-443}"
TIMEOUT="${TIMEOUT:-5}"
RECONCILE_WAIT="${RECONCILE_WAIT:-15}"
EXPIRY_SECS="${EXPIRY_SECS:-45}"
JOURNAL_UNIT="${JOURNAL_UNIT:-containarium}"
CTR="${CTR:-containarium}"
BPF_OBJ="${BPF_OBJ:-/etc/containarium/netpolicy.bpf.o}"

CTR_ARGS=(--server "$DAEMON" --token "$JWT")

PASS=0 FAIL=0 SKIP=0
pass() { printf '  \033[32mPASS\033[0m %s\n' "$*"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAIL=$((FAIL + 1)); }
skip() { printf '  \033[33mSKIP\033[0m %s\n' "$*"; SKIP=$((SKIP + 1)); }
info() { printf '  ·    %s\n' "$*"; }
phase() { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required tool: $1" >&2; exit 2; }; }

# --- probes (always redirect </dev/null on incus exec: a piped script would
# otherwise have its remaining bytes drained as the exec'd process's stdin). ---
icmp_reachable() {
  incus exec "$CONTAINER" -- ping -c1 -W "$TIMEOUT" "$PROBE_IP" </dev/null >/dev/null 2>&1
}
# tcp_reachable: true if the container can open PROBE_IP:PROBE_PORT. Uses whatever
# the container has (curl, then nc). Returns 2 if neither tool exists (→ skip).
tcp_reachable() {
  incus exec "$CONTAINER" -- sh -c '
    if command -v curl >/dev/null 2>&1; then
      curl -sS --max-time '"$TIMEOUT"' -o /dev/null "https://'"$PROBE_IP"':'"$PROBE_PORT"'"
    elif command -v nc >/dev/null 2>&1; then
      nc -z -w'"$TIMEOUT"' '"$PROBE_IP"' '"$PROBE_PORT"'
    else
      exit 99
    fi' </dev/null >/dev/null 2>&1
}

audit_saw_virtual_patch() {
  # best-effort: the daemon logs "[netpolicy] virtual-patch:" and writes audit
  # action network_policy.virtual_patch on each matched deny event.
  command -v journalctl >/dev/null 2>&1 || return 2
  journalctl -u "$JOURNAL_UNIT" --since "${1:-2 min ago}" 2>/dev/null \
    | grep -qiE 'virtual.?patch'
}

reconcile() { sleep "$RECONCILE_WAIT"; }

SNAPSHOT="" HAD_POLICY=0
cleanup() {
  set +e
  phase "Cleanup"
  # Remove every deny rule we added.
  for spec in "${ADDED_DENY[@]:-}"; do
    [ -n "$spec" ] || continue
    # shellcheck disable=SC2086
    "$CTR" "${CTR_ARGS[@]}" network-policy patch rm "$TENANT" $spec >/dev/null 2>&1 \
      && info "removed deny: $spec"
  done
  if [ "$HAD_POLICY" = 1 ]; then
    info "left the tenant's original policy in place (deny rules cleared)"
  else
    "$CTR" "${CTR_ARGS[@]}" network-policy delete "$TENANT" >/dev/null 2>&1 \
      && info "deleted the throwaway policy this run created"
  fi
}
trap cleanup EXIT
declare -a ADDED_DENY=()

# ---------------------------------------------------------------------------
phase "Preflight"
need jq; need incus; need "$CTR"
need curl

[ "${BUILD:-0}" = 1 ] && {
  need clang
  info "compiling $SCRIPT_DIR/netpolicy.bpf.c → $BPF_OBJ"
  clang -O2 -g -target bpfel -I"/usr/include/$(uname -m)-linux-gnu" \
    -c "$SCRIPT_DIR/netpolicy.bpf.c" -o "$BPF_OBJ"
  info "built; point CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT=$BPF_OBJ and restart the daemon, then re-run without BUILD=1"
}

# Daemon reachable.
curl -fsS "$DAEMON/healthz" >/dev/null 2>&1 && pass "daemon reachable at \$DAEMON" \
  || { fail "daemon not reachable at \$DAEMON"; exit 1; }

# Container exists + running.
if incus info "$CONTAINER" >/dev/null 2>&1; then
  pass "container \$CONTAINER exists"
else
  fail "container \$CONTAINER not found"; exit 1
fi

# The loaded object must carry the deny_cidr map, else deny rules can't enforce.
if [ "${SKIP_MAP_CHECK:-0}" != 1 ]; then
  if command -v bpftool >/dev/null 2>&1 && bpftool map show 2>/dev/null | grep -q 'deny_cidr'; then
    pass "loaded BPF object carries the deny_cidr map (#660)"
  else
    fail "deny_cidr map not found in a loaded BPF program — rebuild netpolicy.bpf.o from this branch and restart the daemon (or set SKIP_MAP_CHECK=1 to force). bpftool may also just be absent."
    info "if bpftool is absent the daemon log line on startup is the fallback signal: a configured deny rule with no map logs 'lacks the deny_cidr map (rebuild …)'."
  fi
fi

# Snapshot any existing policy so cleanup can restore/delete appropriately.
if "$CTR" "${CTR_ARGS[@]}" network-policy get "$TENANT" --json >/tmp/np_snapshot.json 2>/dev/null; then
  HAD_POLICY=1; SNAPSHOT="$(cat /tmp/np_snapshot.json)"
  info "snapshotted existing policy for \$TENANT (will be left in place, deny rules cleared)"
fi

# ---------------------------------------------------------------------------
phase "Phase 0 — baseline: allow-all enforce policy, probe reachable"
# Allow everything + arm enforce-mode INTENT (actual drops still require the
# daemon's CONTAINARIUM_NETWORK_POLICY_ENFORCE=1 — detected below).
"$CTR" "${CTR_ARGS[@]}" network-policy set "$TENANT" --mode enforce --egress-cidr 0.0.0.0/0 >/dev/null
reconcile
if icmp_reachable; then
  pass "baseline: container reaches \$PROBE_IP (allowed by 0.0.0.0/0)"
else
  fail "baseline: \$PROBE_IP unreachable BEFORE any deny rule — check the probe / allow-list / container egress; aborting"
  exit 1
fi

# ---------------------------------------------------------------------------
phase "Phase 1 — deny beats allow (whole host)"
SINCE="$(date '+%Y-%m-%d %H:%M:%S' 2>/dev/null || echo '2 min ago')"
"$CTR" "${CTR_ARGS[@]}" network-policy patch add "$TENANT" --cidr "$PROBE_IP/32" --note "CVE-validate-$$" >/dev/null
ADDED_DENY+=("--cidr $PROBE_IP/32")
reconcile

ARMED=unknown
if icmp_reachable; then
