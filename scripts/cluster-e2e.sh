#!/usr/bin/env bash
#
# cluster-e2e.sh — the gated e2e lane for managed Kubernetes clusters
# (#1418). Builds the CLI+daemon from this tree, starts a real daemon
# against a real Postgres on a real Incus host, and runs the six-step
# MVP journey in test/e2e/cluster/ against it.
#
# Local use:   sudo -v && bash scripts/cluster-e2e.sh
# Sabotage:    CONTAINARIUM_E2E_SABOTAGE=join-token bash scripts/cluster-e2e.sh
#              (expected to exit non-zero — that is the point; the
#              workflow's prove-can-fail job asserts it)
# Container:   CONTAINARIUM_E2E_ISOLATION=container \
#              CONTAINARIUM_CLUSTER_ALLOW_CONTAINER_NODES=true \
#              bash scripts/cluster-e2e.sh
# CI:          .github/workflows/cluster-e2e.yml on the self-hosted
#              incus+kvm runner (nightly + release tags, not per-PR), and
#              .github/workflows/cluster-container-e2e.yml on a
#              GitHub-hosted runner for the container class (#1430).
#
# Isolation (#1430): CONTAINARIUM_E2E_ISOLATION selects the class the
# journey runs in — unset/`vm` (default, unchanged) or `container`.
# Container mode also needs the operator opt-in
# CONTAINARIUM_CLUSTER_ALLOW_CONTAINER_NODES=true, which this script only
# passes through: whether a host may weaken the tenant boundary is the
# operator's assertion to make, never the test script's.
#
# Host requirements (the runner-provisioning half of #1418):
#   - VM mode: capacity for the journey's worst case — ~16 vCPU (CP 2 +
#     up to 3×small 2 + one large 8), ~32 GB RAM, ~500 GB free in the
#     Incus pool. On a smaller host the daemon's CPU-admission gate will
#     correctly REFUSE the larger-class scale-up and the lane goes red
#     for an environmental reason that looks like a product bug.
#   - Incus with a usable storage pool at /var/lib/incus/unix.socket
#   - VM mode only: KVM (/dev/kvm). Container-mode nodes are Incus
#     system containers, which is why that lane can run KVM-less on a
#     GitHub-hosted runner; its preconditions (nesting, cgroup v2,
#     br_netfilter, overlay) are probed by the daemon itself, which
#     refuses loudly rather than provisioning a broken node.
#   - Go toolchain, passwordless sudo for the invoking user
#   - Postgres reachable via CONTAINARIUM_POSTGRES_URL, or Docker/Podman
#     available so the script can start a throwaway one
#   - Egress to github.com (k3s binary) and registry.k8s.io (CA, images)
#
# The Go test's exit status is the script's exit status — nothing here
# pipes it away (a green lane must be evidence the test ran).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
# shellcheck source=/dev/null
. "$REPO_ROOT/scripts/lib/wait-daemon-ready.sh"

GRPC_PORT="${CONTAINARIUM_E2E_GRPC_PORT:-15051}"
HTTP_PORT="${CONTAINARIUM_E2E_HTTP_PORT:-18080}"
ISOLATION="${CONTAINARIUM_E2E_ISOLATION:-vm}"
# The Go-side bound. Kept a knob so a lane whose job timeout is tighter
# than the KVM lane's 150m can still let the test time out first and
# print its diagnostics; the default is the KVM lane's unchanged value.
GO_TIMEOUT="${CONTAINARIUM_E2E_GO_TIMEOUT:-110m}"
WORKDIR="$(mktemp -d)"
BIN="$WORKDIR/containarium"
DAEMON_LOG="$WORKDIR/daemon.log"
PG_CONTAINER=""
DAEMON_PID=""

# The tenant/cluster names the Go test uses; the sweep below must match.
# Instances, not VMs: in container mode the same names are Incus system
# containers, and `incus list` reports both classes (#1430).
INSTANCE_PREFIX="e2etenant-k8s-lane-"

log() { echo "==> $*"; }

fail() { echo "FATAL: $*" >&2; exit 1; }

# sweep_leftover_instances deletes every Incus instance (container or VM
# — `incus list` reports both classes, #1430) whose name starts with
# INSTANCE_PREFIX. Called both pre-flight and on exit (#1814): this
# script's own daemon+Postgres are fresh every run (a throwaway Postgres
# container, started below), but the Incus host underneath them is
# shared and persistent across runs on the self-hosted runner. Node
# names/labels are deterministic (same tenant+cluster every run), so the
# daemon's reconciler observes Incus directly (ClusterVMs) rather than
# its own store — a leftover instance from a run whose exit trap never
# got to run (job timeout, hard kill) is silently ADOPTED by the next
# run's reconciler as already-provisioned, inheriting whatever broken
# state it was left in instead of being recreated fresh. The exit trap
# alone cannot guarantee this away, since it is exactly what a hard kill
# skips; pre-flight is what makes a clean slate unconditional.
sweep_leftover_instances() {
  for inst in $(sudo incus list --format csv --columns n 2>/dev/null | grep "^${INSTANCE_PREFIX}" || true); do
    log "sweeping leftover instance $inst"
    sudo incus delete --force "$inst" 2>/dev/null
  done
}

# --- pre-flight: fail loudly, never skip silently -----------------------
case "$ISOLATION" in
  vm)
    [ -e /dev/kvm ] || fail "no /dev/kvm — VM-mode nodes need a KVM-capable host (nested virt on a VM runner)"
    ;;
  container)
    # No KVM check: container-mode nodes are system containers. The
    # host's container-node preconditions are the daemon's probe to
    # make (#1429), and it refuses the create rather than provisioning
    # a node that cannot work.
    [ -n "${CONTAINARIUM_CLUSTER_ALLOW_CONTAINER_NODES:-}" ] \
      || fail "container mode needs the operator opt-in CONTAINARIUM_CLUSTER_ALLOW_CONTAINER_NODES=true; without it the daemon refuses container creates and the lane goes red for a configuration reason"
    ;;
  *)
    fail "unknown CONTAINARIUM_E2E_ISOLATION=$ISOLATION (want vm or container)"
    ;;
esac
[ -S /var/lib/incus/unix.socket ] || fail "no Incus socket at /var/lib/incus/unix.socket"
command -v go >/dev/null || fail "no Go toolchain"
sudo -n true 2>/dev/null || fail "needs passwordless sudo (daemon and Incus operations run as root)"

# #1814: guarantee a clean slate before this run creates anything of its
# own, not just hope the previous run's exit trap already did — see
# sweep_leftover_instances' comment above.
sweep_leftover_instances

cleanup() {
  status=$?
  set +e
  if [ -n "$DAEMON_PID" ]; then
    log "stopping daemon (pid $DAEMON_PID)"
    sudo kill "$DAEMON_PID" 2>/dev/null
    sleep 2
    sudo kill -9 "$DAEMON_PID" 2>/dev/null
  fi
  # Best-effort: sweep whatever this run itself leaves behind. Not the
  # only guarantee of a clean slate any more — see the pre-flight call
  # above and sweep_leftover_instances' comment for why.
  sweep_leftover_instances
  if [ -n "$PG_CONTAINER" ]; then
    log "stopping throwaway postgres"
    "$CONTAINER_RUNTIME" rm -f "$PG_CONTAINER" >/dev/null 2>&1
  fi
  if [ $status -ne 0 ] && [ -f "$DAEMON_LOG" ]; then
    # Cluster lines first, over the WHOLE log. The tail alone is not
    # enough: the daemon decides at startup whether the autoscaler and
    # the endpoint publisher are wired at all, and says so once. In run
    # 15 that verdict sat ~200 lines above the tail window, so a lane
    # that failed on a missing scale-up could not answer "was the
    # autoscaler ever enabled?" from its own log.
