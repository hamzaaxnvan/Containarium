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
# default search path doesn't include it, so add it explicitly. Without this
# the build fails on a stock Ubuntu 24.04 with "asm/types.h file not found".
MULTIARCH_INC="/usr/include/$(uname -m)-linux-gnu"
clang -O2 -g -target bpf -I"$MULTIARCH_INC" -c counter.bpf.c -o counter.bpf.o
echo "    OK: counter.bpf.o ($(stat -c%s counter.bpf.o) bytes)"

echo "==> 2/5: attaching to $BRIDGE"
# clsact qdisc is the modern attach point for tc-bpf.
tc qdisc add dev "$BRIDGE" clsact 2>/dev/null || true
# Attach both directions. da = direct-action — return value from
# the BPF program is the tc verdict; no need for an additional
# tc class.
tc filter add dev "$BRIDGE" ingress bpf da obj counter.bpf.o sec classifier/ingress
tc filter add dev "$BRIDGE" egress  bpf da obj counter.bpf.o sec classifier/egress
echo "    OK: tc filters installed"

# bpftool can confirm the program + map landed.
echo "    BPF programs on $BRIDGE:"
tc filter show dev "$BRIDGE" ingress | sed 's/^/      /'
tc filter show dev "$BRIDGE" egress  | sed 's/^/      /'

echo "==> 3/5: spawning two throwaway containers"
incus launch images:ubuntu/24.04 "$CT_A" --quiet
incus launch images:ubuntu/24.04 "$CT_B" --quiet
# Wait for both to get an IP. Incus assigns DHCP from the bridge
# subnet; give it up to 30s.
for i in $(seq 30); do
    IP_A=$(incus list "$CT_A" -c4 --format csv | awk '{print $1}')
    IP_B=$(incus list "$CT_B" -c4 --format csv | awk '{print $1}')
    if [ -n "$IP_A" ] && [ -n "$IP_B" ]; then break; fi
    sleep 1
done
if [ -z "${IP_A:-}" ] || [ -z "${IP_B:-}" ]; then
    echo "    FAIL: containers didn't get IPs within 30s" >&2
    exit 1
fi
echo "    OK: $CT_A=$IP_A  $CT_B=$IP_B"

echo "==> 4/5: snapshotting counters, pinging, re-reading"
# Find the map ID by name. The pkt_counter map name is from the
# BPF C source. cilium/ebpf in production would address it by
# Object spec; bpftool is fine for the throwaway path.
MAP_ID=$(bpftool map show | awk '/name pkt_counter/ {print $1}' | tr -d ':' | head -1)
if [ -z "$MAP_ID" ]; then
    echo "    FAIL: pkt_counter map not visible to bpftool" >&2
    exit 1
fi

read_counter() {
    local idx=$1
    # Map values are little-endian u64. bpftool prints hex bytes
    # separated by spaces; reverse + concatenate + interpret as
    # hex.
    bpftool map lookup id "$MAP_ID" key hex 0$idx 00 00 00 \
        | awk '/value/ {for (i=NF; i>1; i--) printf "%s", $i; print ""}' \
        | sed 's/[^0-9a-f]//g' \
        | xargs -I{} printf "%d\n" "0x{}"
}

BEFORE_IN=$(read_counter 0)
BEFORE_EG=$(read_counter 1)
echo "    before: ingress=$BEFORE_IN egress=$BEFORE_EG"

# 5 pings from A → B. Should produce 10 packets on each direction
# (5 echo request + 5 echo reply), minus whatever Incus management
# traffic happens concurrently. Just assert "more than before."
incus exec "$CT_A" -- ping -c 5 -W 2 "$IP_B" >/dev/null || true

AFTER_IN=$(read_counter 0)
AFTER_EG=$(read_counter 1)
echo "    after:  ingress=$AFTER_IN egress=$AFTER_EG"

echo "==> 5/5: assertions"
PASS=1
if [ "$AFTER_IN" -le "$BEFORE_IN" ]; then
    echo "    FAIL: ingress counter did not increment" >&2
    PASS=0
fi
if [ "$AFTER_EG" -le "$BEFORE_EG" ]; then
