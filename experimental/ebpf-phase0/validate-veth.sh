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
