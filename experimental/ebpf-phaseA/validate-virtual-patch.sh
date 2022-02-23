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
