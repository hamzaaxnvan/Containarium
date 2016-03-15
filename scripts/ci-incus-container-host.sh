#!/usr/bin/env bash
#
# ci-incus-container-host.sh — turn a stock GitHub-hosted `ubuntu-latest`
# runner into a host that can run Containarium CONTAINER-mode cluster
# nodes (#1430). Used by .github/workflows/cluster-container-e2e.yml,
# whose two jobs both need it.
#
# Follows the incus-create.yml precedent, minus KVM: container-mode
# cluster nodes are Incus system containers, so no /dev/kvm is involved
# anywhere below. This host CANNOT run VM-mode clusters, which is why
# that lane stays on the self-hosted incus+kvm runner.
#
# Every precondition is asserted here, at the step that provisions it,
# rather than discovered minutes later inside a product test — an
# unusable runner should name itself, not wear the costume of a bug.
set -euo pipefail

log() { echo "==> $*"; }
fail() { echo "::error::$*"; exit 1; }

log "installing Incus, dnsmasq and ZFS"
sudo apt-get update
sudo apt-get install -y --no-install-recommends incus dnsmasq-base zfsutils-linux
# The module ships in linux-modules-extra on Ubuntu; usually already
# present on the runner image, so only fetch it if needed.
if ! sudo modprobe zfs 2>/dev/null; then
  log "zfs module not loadable yet; installing linux-modules-extra-$(uname -r)"
  sudo apt-get install -y "linux-modules-extra-$(uname -r)"
  sudo modprobe zfs
fi

# The pool is named incus-local with a `containers` dataset because that
# is what the daemon's OWN storage detection probes for; under any other
# name it falls back to the `dir` driver, which cannot honour the node
# groups' disk sizes. /mnt is the runner's large scratch disk and the
# image is sparse.
log "creating the ZFS pool the daemon detects"
sudo truncate -s 40G /mnt/incus-zfs.img
sudo zpool create -f -m /mnt/incus-local incus-local /mnt/incus-zfs.img
sudo zfs create incus-local/containers

# Kernel modules the daemon's container-node probe (#1429) requires:
# br_netfilter for k3s networking, overlay for containerd's snapshotter.
# Loaded here — on a shared kernel the daemon cannot load them itself.
log "loading the kernel modules k3s-in-container needs"
sudo modprobe br_netfilter
sudo modprobe overlay

# Two runner-specific facts that would otherwise surface as a mystifying
# failure minutes into the journey:
#  1. Ubuntu restricts unprivileged user namespaces via AppArmor, which
#     is what an unprivileged Incus container needs.
#  2. Docker is running on GitHub runners and sets the iptables FORWARD
#     policy to DROP, which silently kills incusbr0's NAT — nodes then
#     boot with no route out and fail while fetching k3s.
log "making the runner able to host containers"
sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 || true
sudo iptables -P FORWARD ACCEPT
grep -q '^root:' /etc/subuid || echo 'root:1000000:1000000000' | sudo tee -a /etc/subuid
grep -q '^root:' /etc/subgid || echo 'root:1000000:1000000000' | sudo tee -a /etc/subgid
