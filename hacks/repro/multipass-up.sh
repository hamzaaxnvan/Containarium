#!/usr/bin/env bash
#
# multipass-up.sh — provision a throwaway Ubuntu VM (via Multipass) to run the
# #301 sshpiper-reload harness. Intended for local dev on macOS/Linux where
# sshpiperd can't run on the host (e.g. macOS).
#
# WHY MULTIPASS
#   The harness needs Linux + root + sshpiperd + sshd. Multipass gives a clean
#   Ubuntu 24.04 VM in ~30s. On an Intel Mac, VirtualBox/Vagrant also work, but
#   Multipass is lighter and pairs naturally with the Ubuntu/Incus stack.
#
# USAGE
#   ./multipass-up.sh           # launch (or reuse) the VM and copy the harness in
#   ./multipass-up.sh --run     # ... then run the harness and stream its output
#   ./multipass-up.sh --shell   # ... then drop into a shell
#   ./multipass-up.sh --down     # delete + purge the VM
#
# OVERRIDES (env)
#   VM=sp301  CPUS=2  MEM=2G  DISK=10G  IMAGE=24.04
#
# To test PRODUCTION's exact sshpiperd build, install it in the VM and run the
# harness with SSHPIPERD_BIN / SSHPIPERD_LAUNCH set (see the harness header).
#
set -euo pipefail

VM="${VM:-sp301}"
CPUS="${CPUS:-2}"
MEM="${MEM:-2G}"
DISK="${DISK:-10G}"
IMAGE="${IMAGE:-24.04}"

HERE="$(cd "$(dirname "$0")" && pwd)"
HARNESS="$HERE/sshpiper-reload-301.sh"
CLOUD_INIT="$HERE/cloud-init.yaml"
GUEST_HARNESS="/home/ubuntu/sshpiper-reload-301.sh"

die() { echo "error: $*" >&2; exit 1; }

command -v multipass >/dev/null || die "multipass not installed — 'brew install --cask multipass' (macOS) or https://multipass.run"
[ -f "$HARNESS" ] || die "harness not found at $HARNESS"

case "${1:-}" in
  --down)
    multipass delete --purge "$VM" 2>/dev/null && echo "deleted $VM" || echo "$VM not present"
