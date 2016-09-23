#!/usr/bin/env bash
#
# netpolicy-split-egress-e2e.sh — proves the split egress mode (#2970 / #2974)
# end to end with the daemon's enforce switch ON (#2981): a real daemon, the
# real eBPF object, two real tenant containers on one Incus bridge, real
# packets, and the audit rows the daemon writes from each deny event's
# verdict byte.
#
# A tenant's policy has two modes. `mode` governs the cross-tenant,
# intra-tenant and metadata checks; `egress_mode` governs only a miss on the
# egress allow-list (experimental/ebpf-phaseA/netpolicy.bpf.c, netpolicy_ingress:
# `deny_mode = cfg->egress_mode` on an allow-list miss). The scenario:
#
#   phase 1  A: mode=ENFORCE, egress_mode=LOG_ONLY
#            A -> B             dropped, audited network_policy.deny_dropped
#            A -> unlisted host passes,  audited network_policy.deny_logged
#   phase 2  A: mode=LOG_ONLY, egress_mode=ENFORCE (the reverse split)
#            A -> unlisted host dropped, audited network_policy.deny_dropped
#            A -> B             follows mode: passes, audited deny_logged
#   both     B (no stored policy) keeps its reachability and is never dropped.
#
# Why the audit action proves the verdict byte: OnDenyEvent labels a row from
# the verdict the program reported, and falls back to the tenant-wide `mode`
# only for an event without one (network_policy_enforcer.go,
# denyEventDropped). In both split phases that fallback gives the OPPOSITE
# label for the allow-list miss, so `deny_logged` in phase 1 and
# `deny_dropped` in phase 2 can only come from the verdict byte.
#
# The "unlisted host" is the bridge gateway (the runner host itself): it is
# not a managed container (so it reaches the allow-list branch, not the
# cross-tenant one), A's policy lists no egress CIDR and no preset (so the
# gateway is not implicitly allowed), and it answers ICMP without any
# internet dependency. ICMP is used throughout so neither box needs a
# listener installed.
#
# The enforcer is sender-side with no connection tracking: a reply is policed
# as the replier's own egress. So "B is unaffected" is asserted on B's own
# egress and on B's audit verdicts, not on B -> A (A's echo replies are A -> B
# traffic and follow A's `mode`).
#
# Preconditions (the workflow sets them; the script checks what it can):
#   - the daemon runs with CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT set,
#     CONTAINARIUM_NETWORK_POLICY_ENFORCE=1 and --postgres;
#   - CONTAINARIUM_TENANT_GUARD=off, so the eBPF enforcer is the only filter
#     between the two boxes (the guard's Incus NIC ACL drops cross-tenant
#     ingress regardless of policy, which would hide phase 2's "A -> B
#     follows mode"; the guard has its own lane, tenant-guard-e2e.yml);
#   - containers <tenant-a>-container and <tenant-b>-container exist, owned
#     by the two tenants, and B has no stored network policy.
#
# Usage (on the lane host, with incus access via sudo):
#   scripts/netpolicy-split-egress-e2e.sh <tenant-a> <tenant-b>
#
# Environment:
#   SERVER                      daemon HTTP address (required)
#   CONTAINARIUM_TOKEN          admin token (required; read by the CLI)
#   CONTAINARIUM_HTTP=true      talk REST (read by the CLI)
#   CONTAINARIUM_POSTGRES_URL   audit store DSN for `audit query` (required)
#   CONTAINARIUM_CLI            CLI binary (default /tmp/containarium)
#
# Exit status: 0 when every assertion held, 1 on the first that did not.

set -euo pipefail

A=${1:?usage: $0 <tenant-a> <tenant-b>}
B=${2:?usage: $0 <tenant-a> <tenant-b>}
CLI=${CONTAINARIUM_CLI:-/tmp/containarium}
: "${SERVER:?SERVER (daemon HTTP address) is required}"
: "${CONTAINARIUM_TOKEN:?CONTAINARIUM_TOKEN (admin token) is required}"
: "${CONTAINARIUM_POSTGRES_URL:?CONTAINARIUM_POSTGRES_URL (audit store) is required}"
AC="${A}-container"
BC="${B}-container"

fail() { echo "::error::$*"; exit 1; }
step() { echo; echo "=== $* ==="; }
cli() { "$CLI" --server "$SERVER" "$@"; }
now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

for tool in jq sudo; do
  command -v "$tool" >/dev/null || fail "$tool is required"
done
test -x "$CLI" || fail "CLI binary $CLI is missing"

# ---------------------------------------------------------------------------
# Reachability probes. ping exits 0 only when a reply came back.
# ---------------------------------------------------------------------------

# reaches <container> <dst>: retried, because a fresh bridge or ARP entry can
# lose the first echo; a single success is enough.
reaches() {
  local c=$1 dst=$2
  for _ in 1 2 3; do
    if sudo incus exec "$c" -- ping -c 2 -W 2 "$dst" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

expect_reach() {
  local what=$1 c=$2 dst=$3
  if reaches "$c" "$dst"; then
    echo "ok: $what ($c -> $dst) passes"
  else
    fail "$what: $c -> $dst got no reply, want it to pass"
  fi
}

# expect_blocked: one burst of 3 echoes; ANY reply is a failure.
expect_blocked() {
  local what=$1 c=$2 dst=$3
  if sudo incus exec "$c" -- ping -c 3 -W 2 "$dst" >/dev/null 2>&1; then
    fail "$what: $c -> $dst got a reply, want it dropped"
  fi
  echo "ok: $what ($c -> $dst) dropped"
}

# ---------------------------------------------------------------------------
# Audit rows. `audit query --raw` is tab-separated:
# timestamp, username, action, resource_type, resource_id, status, detail.
# ---------------------------------------------------------------------------

# audit_rows <action> <tenant> <dst> <from>: matching rows, one per line.
audit_rows() {
  local action=$1 tenant=$2 dst=$3 from=$4 out
  out=$("$CLI" audit query --resource-type network_policy --action "$action" \
          --from "$from" --limit 1000 --raw) || fail "audit query failed (action=$action)"
  printf '%s\n' "$out" | awk -F'\t' -v t="$tenant" '$5 == t' | grep -F "\"dst\":\"$dst\"" || true
}

# expect_audit: poll up to 30s for at least one matching row.
expect_audit() {
  local action=$1 tenant=$2 dst=$3 from=$4 rows
  for _ in $(seq 1 15); do
    rows=$(audit_rows "$action" "$tenant" "$dst" "$from")
    if [ -n "$rows" ]; then
      echo "ok: audited $action for $tenant -> $dst"
      printf '%s\n' "$rows" | head -1
      return 0
    fi
    sleep 2
  done
  echo "--- every network_policy audit row since $from ---"
  "$CLI" audit query --resource-type network_policy --from "$from" --limit 200 --raw || true
  fail "no $action audit row for $tenant -> $dst since $from"
}

# expect_no_audit: call only after a positive expect_audit of the same flow,
# so the deny events of the probe have already been written.
expect_no_audit() {
  local action=$1 tenant=$2 dst=$3 from=$4 rows
  rows=$(audit_rows "$action" "$tenant" "$dst" "$from")
  if [ -n "$rows" ]; then
    printf '%s\n' "$rows"
    fail "unexpected $action audit row(s) for $tenant -> $dst since $from"
