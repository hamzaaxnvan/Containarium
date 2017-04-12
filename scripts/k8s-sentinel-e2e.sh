#!/usr/bin/env bash
#
# k8s-sentinel-e2e.sh — prove the sentinel→K8s-node SSH chain end to end:
#
#   agent ──agent key──▶ sentinel sshpiper ──sentinel upstream key──▶
#     node in-cluster sshpiper (NodePort) ──node upstream key──▶ box pod → MCP
#
# A kind cluster stands in for the K8s node (its in-cluster sshpiper is the
# node gateway); a second sshpiperd, run directly on the runner with a
# generated yaml config, stands in for the fleet sentinel. Both hops are
# real sshpiperd; only the daemon is faked (we program the node gateway's
# Pipe + the box Secret directly rather than running the Containarium daemon,
# which would need Postgres etc.).
#
# Status: manual/local verification tool. Not yet wired into a required CI
# gate — the stand-in sentinel's exact sshpiperd yaml-plugin invocation needs
# a live shakeout on a real runner before it becomes a gate. The unit tests
# (tunnel Forward map, ResolveGatewayDialTarget) and the existing k8s-e2e
# cover the code paths; this script proves the full two-hop chain end to end.
#
# Local use:    bash scripts/k8s-sentinel-e2e.sh
# Keep cluster: E2E_KEEP=1 bash scripts/k8s-sentinel-e2e.sh
#
# Requirements: kind, kubectl, docker, ssh, ssh-keygen, base64 (all on the
# GitHub ubuntu-latest runner). The node gateway image is
# ghcr.io/footprintai/containarium-sshpiper (published per release); the box
# image is ghcr.io/footprintai/containarium-agent-box. Override with
# AGENT_BOX_IMAGE / SSHPIPER_IMAGE.
set -euo pipefail

CLUSTER="${KIND_CLUSTER:-containarium-sentinel-e2e}"
AGENT_SANDBOX_VERSION="${AGENT_SANDBOX_VERSION:-v0.5.1}"
AGENT_BOX_IMAGE="${AGENT_BOX_IMAGE:-ghcr.io/footprintai/containarium-agent-box:latest-stable}"
SSHPIPER_IMAGE="${SSHPIPER_IMAGE:-ghcr.io/footprintai/containarium-sshpiper:latest-stable}"
KEYDIR="$(mktemp -d)"
SENTINEL_PORT=32222 # host port the "sentinel" sshpiperd listens on
NODE_NODEPORT=32022 # kind NodePort for the node gateway

cleanup() {
  [ -n "${SENTINEL_PID:-}" ] && kill "${SENTINEL_PID}" 2>/dev/null || true
  if [ "${E2E_KEEP:-}" != "1" ]; then
    kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
  fi
  rm -rf "$KEYDIR"
}
trap cleanup EXIT

echo "==> keys: agent, sentinel-upstream, node-upstream, host keys"
ssh-keygen -t ed25519 -f "$KEYDIR/agent" -N "" -q
ssh-keygen -t ed25519 -f "$KEYDIR/sentinel_upstream" -N "" -q # sentinel → node gateway
ssh-keygen -t ed25519 -f "$KEYDIR/node_upstream" -N "" -q     # node gateway → box
ssh-keygen -t ed25519 -f "$KEYDIR/sentinel_host" -N "" -q
ssh-keygen -t ed25519 -f "$KEYDIR/node_host" -N "" -q

echo "==> kind cluster + agent-sandbox controller + Pipe CRD"
kind create cluster --name "$CLUSTER" --config - >/dev/null <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: ${NODE_NODEPORT}
        hostPort: ${NODE_NODEPORT}
EOF
kubectl apply -f "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/manifest.yaml" >/dev/null
kubectl -n agent-sandbox-system wait --for=condition=available deployment/agent-sandbox-controller --timeout=180s
kubectl apply -f "https://raw.githubusercontent.com/tg123/sshpiper/master/plugin/kubernetes/crd.yaml" >/dev/null
kubectl create namespace agent-gateway >/dev/null

echo "==> node gateway (in-cluster sshpiper) on NodePort ${NODE_NODEPORT}"
kubectl -n agent-gateway create secret generic sshpiper-server-key --from-file=server_key="$KEYDIR/node_host" >/dev/null
kubectl -n agent-gateway create secret generic node-upstream-key --type=kubernetes.io/ssh-auth \
  --from-file=ssh-privatekey="$KEYDIR/node_upstream" >/dev/null
kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: ServiceAccount
metadata: {name: sshpiper, namespace: agent-gateway}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: sshpiper, namespace: agent-gateway}
rules:
  - {apiGroups: [sshpiper.com], resources: [pipes], verbs: [get, list, watch]}
  - {apiGroups: [""], resources: [secrets], verbs: [get, list, watch]}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: sshpiper, namespace: agent-gateway}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: sshpiper}
subjects: [{kind: ServiceAccount, name: sshpiper, namespace: agent-gateway}]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: sshpiper, namespace: agent-gateway}
spec:
  replicas: 1
  selector: {matchLabels: {app: sshpiper}}
  template:
    metadata: {labels: {app: sshpiper}}
    spec:
      serviceAccountName: sshpiper
      containers:
        - name: sshpiper
          image: ${SSHPIPER_IMAGE}
          env:
            - {name: PLUGIN, value: kubernetes}
            - {name: SSHPIPERD_SERVER_KEY, value: /serverkey/ssh_host_ed25519_key}
          ports: [{name: ssh, containerPort: 2222}]
          volumeMounts: [{name: server-key, mountPath: /serverkey/, readOnly: true}]
      volumes:
        - name: server-key
          secret:
