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
		;;
	-h | --help)
		grep '^#' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		die "unknown argument: $1"
		;;
	esac
done
[[ -n "$VM_NAME" ]] || die "usage: $0 --name <vm-name>"

load_config
require_vars VM_CPUS VM_MEM_MB VM_DISK_GB INCUS_IMAGE BENCH_SSH_PUBKEY_FILE REMOTE_SSH_USER

command -v incus >/dev/null 2>&1 || die "incus not found — this must run on the hypervisor host"
[[ -f "$BENCH_SSH_PUBKEY_FILE" ]] || die "BENCH_SSH_PUBKEY_FILE ($BENCH_SSH_PUBKEY_FILE) not found — generate a host->guest keypair first: ssh-keygen -t ed25519 -N '' -f <path>"

if sudo incus list --format csv -c n | grep -qx "$VM_NAME"; then
	die "a VM named '${VM_NAME}' already exists — run 99-teardown-vm.sh first, or pick a different --name"
fi

log "creating Incus VM '${VM_NAME}' (cpus=${VM_CPUS} mem=${VM_MEM_MB}MiB disk=${VM_DISK_GB}GB, image=${INCUS_IMAGE})"
sudo incus launch "$INCUS_IMAGE" "$VM_NAME" --vm \
	-c limits.cpu="$VM_CPUS" \
	-c limits.memory="${VM_MEM_MB}MiB" \
	-d root,size="${VM_DISK_GB}GB"

log "waiting for the Incus agent to be reachable inside the guest"
ready=0
for _ in $(seq 1 60); do
	if sudo incus exec "$VM_NAME" -- true 2>/dev/null; then
		ready=1
		break
	fi
	sleep 3
done
[[ "$ready" == 1 ]] || die "VM '${VM_NAME}' never became exec-reachable — check 'incus console ${VM_NAME}'"

