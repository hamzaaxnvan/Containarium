#!/bin/bash
#
# Create an Incus VM with a hard CPU/memory/disk cap for the
# sandbox-density benchmark. Incus VMs are KVM-backed — limits.cpu /
# limits.memory are a true hardware-partitioned ceiling on the guest, the
# same guarantee a VirtualBox VM would give (see README.md "Hard resource
# caps"). Uses Incus rather than VirtualBox deliberately: running a second
# hypervisor's kernel module (vboxdrv) alongside an already-live KVM/Incus
# host is an avoidable stability risk, and Incus already provides the same
# hard-cap guarantee on a stack that's already proven on this class of
# host.
#
# Boots a stock VM image (images:ubuntu/24.04) and sets up SSH access
# directly via `incus exec`/`incus file push` — NOT cloud-init. Confirmed
# live on the first real run: the linuxcontainers.org `images:` remote's
# ubuntu/24.04 VM image ships neither cloud-init nor openssh-server
# installed (unlike an official Ubuntu cloud image), so a
# cloud-init.user-data config is silently a no-op on it. incus exec/file
# push works unconditionally against any VM the Incus agent can reach,
# regardless of what the image does or doesn't have installed.
#
# Runs locally on whatever machine has Incus installed. For a remote host,
# SSH in yourself and run this script there — it deliberately does not
# embed a remote host name (see CLAUDE.md's anonymization convention).
#
# Usage:
#   scripts/00-create-vm.sh --name <vm-name>

set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=lib.sh
source ./lib.sh

VM_NAME=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--name)
		VM_NAME="$2"
		shift 2
