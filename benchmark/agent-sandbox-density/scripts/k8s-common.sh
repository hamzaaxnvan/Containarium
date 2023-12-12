#!/bin/bash
#
# Shared kubeadm + CNI + gVisor + agent-sandbox-controller provisioning.
# Both sides of this benchmark run on Kubernetes, with gVisor installed and
# every Sandbox pod scheduled under it — the control group creates `Sandbox`
# CRs directly (kubectl), the experiment group creates them indirectly via
# the Containarium daemon (see README.md "What's actually under test").
# gVisor being present on BOTH sides is deliberate: it removes gVisor
# itself as a variable, so what the benchmark actually isolates is
# Containarium's own orchestration cost (the extra daemon hop, and the
# request==limit CLI behavior — see README.md "Fairness notes"), not
# "does gVisor cost density" mixed in with it. Any drift between the two
# sides in this shared setup would confound that. Sourced by
# 01-provision-k8s-control.sh and 03-provision-containarium.sh, not
# executed directly.

set -euo pipefail

provision_base_k8s() {
	log "installing containerd + kubeadm/kubelet/kubectl"
	ssh_or_local "sudo bash -s" <<'REMOTE'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

apt-get update -qq
apt-get install -y -qq apt-transport-https ca-certificates curl gpg containerd

mkdir -p /etc/containerd
containerd config default | sudo tee /etc/containerd/config.toml >/dev/null
sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
systemctl restart containerd

install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.30/deb/Release.key |
	gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.30/deb/ /' |
	tee /etc/apt/sources.list.d/kubernetes.list >/dev/null
apt-get update -qq
apt-get install -y -qq kubelet kubeadm kubectl
apt-mark hold kubelet kubeadm kubectl

swapoff -a
sed -i '/ swap /s/^/#/' /etc/fstab

cat <<EOF >/etc/modules-load.d/k8s.conf
overlay
br_netfilter
EOF
