#!/usr/bin/env bash
#
# tenant-guard-legit-flows-e2e.sh — the POSITIVE half of the tenant network
# guard's acceptance gate (docs/architecture/tenant-network-guard.md).
#
# scripts/tenant-tenant-network-isolation-e2e.sh proves tenants are kept
# apart. This script proves the guard did not also break what must keep
# working: a tenant's own boxes reach each other, the host reaches every
# box, Caddy reaches a box, a box reaches Caddy and the internet, and a
# guarded box still renews its DHCP lease. Every expected drop leaves a
# kernel-log line (security.acls.default.ingress.logged=true). A guard that
# breaks the platform is not a fix.
#
# Run ON the backend host as an operator with incus access, with the guard
# on (the default; CONTAINARIUM_TENANT_GUARD unset or "enforce") and the
# daemon having reconciled:
#   sudo bash scripts/tenant-guard-legit-flows-e2e.sh <a1> <a2> <b>
#
# <a1> and <a2> are two running containers of ONE tenant; <b> is a running
# container of ANOTHER tenant. Use fixture tenants, never real customers.
#
# Expected results (any deviation is a FAIL, exit 1):
#   a1 -> a2:22                       ok    (same tenant)
#   a2 -> a1:22                       ok    (same tenant, other direction)
#   host -> a1:22                     ok    (sentinel / daemon path)
#   core-caddy -> a1:22               ok    (reverse proxy to a box)   [SKIP if no caddy here]
#   a1 -> core-caddy:443              ok    (platform API in-bridge)   [SKIP if no caddy here]
#   a1 -> dhcp renew                  ok    (udhcpc/dhclient against the host)
#   a1 -> b:22                        DROP
#   b  -> a1:22                       DROP
#   each guarded NIC carries containarium-tenant-* in security.acls
#   kernel log carries one line per DROP above

set -uo pipefail

A1="${1:?usage: $0 <a1> <a2> <b>}"
A2="${2:?usage: $0 <a1> <a2> <b>}"
B="${3:?usage: $0 <a1> <a2> <b>}"
FAILS=0
CONNECT_TIMEOUT="${CONNECT_TIMEOUT:-3}"

for c in "$A1" "$A2" "$B"; do
  if ! incus info "$c" >/dev/null 2>&1; then
    echo "FATAL: no such container on this host: $c"; exit 2
  fi
  case "$c" in
    core-*|containarium-core-*) echo "FATAL: $c looks like a core/platform container, not a tenant — refusing."; exit 2 ;;
  esac
done

bridge_ip() {
  local ips
