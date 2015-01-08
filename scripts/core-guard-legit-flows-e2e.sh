#!/usr/bin/env bash
#
# core-guard-legit-flows-e2e.sh — the POSITIVE half of the core-infra network
# guard's acceptance gate (docs/architecture/core-infra-network-guard.md).
#
# scripts/tenant-core-infra-network-isolation-e2e.sh proves tenants are kept
# OUT. This script proves the guard did not also lock the platform out of
# itself: every flow the guard's table (internal/coreguard) deliberately
# allows still works, every flow it deliberately drops is dropped, and each
# drop leaves a kernel-log line (security.acls.default.ingress.logged=true).
# A guard that breaks the platform is not a fix.
#
# Run ON the backend host as an operator with incus access, AFTER the guard
# is enabled there (CONTAINARIUM_CORE_GUARD=enforce and the daemon has
# reconciled):
#   sudo bash scripts/core-guard-legit-flows-e2e.sh <tenant-container-name>
#
# <tenant-container-name> must be a real, running, ordinary tenant container
# on this host. Only its network namespace is used, for bare TCP connects.
#
# Expected results (any deviation is a FAIL, exit 1):
#   host gateway     -> core-postgres:5432          ok    (daemon pool)
#   victoriametrics  -> core-postgres:5432          ok    (grafana datastore)
#   tenant           -> core-caddy:443              ok    (platform API in-bridge)
#   tenant           -> core-otelcollector:4318     ok    (tenant OTLP push)
#   tenant           -> core-postgres:5432          DROP
#   tenant           -> core-victoriametrics:3000   DROP  (grafana)
#   tenant           -> core-caddy:2019             DROP  (admin API)
#   kernel log carries one line per DROP above
#
# A core role that is not deployed on this host is reported SKIP for its
# rows, not FAIL — the guard only governs what exists.

set -uo pipefail

TENANT="${1:?usage: $0 <tenant-container-name>}"
FAILS=0
CONNECT_TIMEOUT="${CONNECT_TIMEOUT:-3}"

if ! incus info "$TENANT" >/dev/null 2>&1; then
  echo "FATAL: no such container on this host: $TENANT"
  exit 2
fi
case "$TENANT" in
  core-*|containarium-core-*) echo "FATAL: $TENANT looks like a core/platform container, not a tenant — refusing to use it as the fixture."; exit 2 ;;
esac

# --- discovery ---------------------------------------------------------------

# bridge_ip <container>: the container's address ON THE BRIDGE. A tenant
# running docker/podman also reports 172.17.0.1 (docker0) & co., and
# `incus list` prints those first — so prefer the eth0 entry and only fall
# back to the first IPv4 when there is no eth0 at all.
bridge_ip() {
  local ips
  ips=$(incus list "$1" --format csv -c 4 2>/dev/null)
  echo "$ips" | grep -oE "[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+ \(eth0\)" | head -1 | cut -d' ' -f1 | grep . \
    || echo "$ips" | grep -oE "[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+" | head -1
}

core_ip() { # $1 = role suffix (postgres, caddy, ...)
  bridge_ip "containarium-core-$1"
}

BRIDGE=$(incus list "$TENANT" --format yaml 2>/dev/null | awk '/^ *network: /{print $2; exit}')
if [ -z "$BRIDGE" ]; then
  BRIDGE=$(incus network list --format csv -c n,t 2>/dev/null | awk -F, '$2=="bridge"{print $1; exit}')
fi
HOST_GW=$(incus network get "$BRIDGE" ipv4.address 2>/dev/null | cut -d/ -f1)
[ -z "$HOST_GW" ] && { echo "FATAL: could not determine the host gateway on bridge '$BRIDGE'"; exit 2; }

PG=$(core_ip postgres)
VM=$(core_ip victoriametrics)
CADDY=$(core_ip caddy)
OTEL=$(core_ip otelcollector)
TENANT_IP=$(bridge_ip "$TENANT")

echo "== bridge=$BRIDGE host_gw=$HOST_GW tenant=$TENANT($TENANT_IP)"
echo "== core: postgres=${PG:-absent} victoriametrics=${VM:-absent} caddy=${CADDY:-absent} otelcollector=${OTEL:-absent}"
