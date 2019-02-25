#!/usr/bin/env bash
# Phase 0.5 — per-container veth-attach validation.
#
# Phase 0 (validate.sh) found that tc-bpf on the BRIDGE MASTER (incusbr0) only
# sees one direction of container↔container traffic: the bridge forwards frames
# between ports without them traversing the master's tc-egress hook, so the
# egress counter stayed 0. (#315)
#
# This re-tests the corrected attach point the Phase A design needs: attach to a
# container's HOST-SIDE VETH instead of the bridge. On the host veth:
#   - tc ingress = frames arriving from the container  (its OUTBOUND traffic)
#   - tc egress  = frames the host delivers to the container (its INBOUND)
# so a single A→B ping must bump BOTH counters on A's veth. If it does, the
# per-veth attach point observes both directions and Phase A can build on it.
#
# THROWAWAY — observation only (TC_ACT_OK, no drops); cleanup trap on EXIT.
# Run on a Linux+Incus backend as root, kernel ≥ 6.6.
#
# Usage: sudo ./validate-veth.sh [--keep]
#
set -euo pipefail

KEEP=0
CT_A="phase0-veth-a"
CT_B="phase0-veth-b"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

while [ $# -gt 0 ]; do
    case "$1" in
        --keep) KEEP=1; shift ;;
        --help|-h) sed -n '2,/^$/p' "$0" | sed 's|^# \{0,1\}||'; exit 0 ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done

[ "$(id -u)" -eq 0 ] || { echo "must run as root (need tc, bpftool, incus)" >&2; exit 2; }
[ "$(uname -s)" = "Linux" ] || { echo "Linux only" >&2; exit 2; }
for cmd in clang tc bpftool incus; do
    command -v "$cmd" >/dev/null 2>&1 || { echo "missing required command: $cmd" >&2; exit 2; }
done

VETH_A=""
VETH_B=""
cleanup() {
    set +e
    if [ "$KEEP" -eq 0 ]; then
        echo "==> cleanup"
        [ -n "$VETH_A" ] && tc qdisc del dev "$VETH_A" clsact 2>/dev/null
        [ -n "$VETH_B" ] && tc qdisc del dev "$VETH_B" clsact 2>/dev/null
        rm -f /sys/fs/bpf/phase0_pkt_counter 2>/dev/null
        incus delete --force "$CT_A" </dev/null 2>/dev/null
        incus delete --force "$CT_B" </dev/null 2>/dev/null
        rm -f "$SCRIPT_DIR/counter.bpf.o"
    else
        echo "==> --keep set; leaving veth=$VETH_A attached + containers up"
    fi
}
