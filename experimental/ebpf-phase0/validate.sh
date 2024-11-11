#!/usr/bin/env bash
# Phase 0 — bridge-attach validation runner.
#
# Run this on a Containarium backend VM as root. It:
#   1. Builds counter.bpf.o from counter.bpf.c (needs clang + libbpf headers).
#   2. Loads + attaches the counters to the bridge in TC_INGRESS + TC_EGRESS.
#   3. Spawns two throwaway Incus containers and pings between them.
#   4. Reads the counters and asserts both are non-zero.
#   5. Cleans up — detaches, deletes containers, deletes BPF objects.
#
# THROWAWAY. Not for production wiring. If this passes on a real
# backend, the eBPF network isolation design's bridge-attach
# assumption is validated; if it fails, the design needs revisiting
# before Phase A starts.
#
# Usage:
#   sudo ./validate.sh [--bridge BRIDGE] [--keep]
#
#   --bridge BRIDGE   bridge interface to attach to (default: incusbr0)
#   --keep            don't clean up; leave attached for manual poking
#
# Exit code 0 on success, non-zero on any failed assertion.

set -euo pipefail

BRIDGE="incusbr0"
KEEP=0
CT_A="phase0-validate-a"
CT_B="phase0-validate-b"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

while [ $# -gt 0 ]; do
    case "$1" in
        --bridge) BRIDGE="$2"; shift 2 ;;
        --keep)   KEEP=1; shift ;;
        --help|-h)
            sed -n '2,/^$/p' "$0" | sed 's|^# \{0,1\}||'
            exit 0 ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done

if [ "$(id -u)" -ne 0 ]; then
    echo "must run as root (need tc, bpftool, incus)" >&2
    exit 2
fi

for cmd in clang tc bpftool incus; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        echo "missing required command: $cmd" >&2
        exit 2
    fi
done

cleanup() {
    set +e
    if [ "$KEEP" -eq 0 ]; then
        echo "==> cleanup"
        tc qdisc del dev "$BRIDGE" clsact 2>/dev/null
        bpftool prog show name count_ingress 2>/dev/null | awk '/^[0-9]+:/ {print $1}' \
            | tr -d ':' | xargs -r -I{} bpftool prog detach name count_ingress 2>/dev/null
        rm -f /sys/fs/bpf/phase0_pkt_counter 2>/dev/null
        incus delete --force "$CT_A" 2>/dev/null
        incus delete --force "$CT_B" 2>/dev/null
        rm -f "$SCRIPT_DIR/counter.bpf.o"
    else
        echo "==> --keep set; leaving everything attached"
        echo "    manual cleanup:"
        echo "      tc qdisc del dev $BRIDGE clsact"
        echo "      rm /sys/fs/bpf/phase0_pkt_counter"
        echo "      incus delete --force $CT_A $CT_B"
    fi
}
trap cleanup EXIT

echo "==> 1/5: building BPF object"
cd "$SCRIPT_DIR"
# linux/bpf.h pulls in <asm/types.h>, which on Debian/Ubuntu lives under the
# multiarch dir (/usr/include/x86_64-linux-gnu/asm/) — clang's `-target bpf`
