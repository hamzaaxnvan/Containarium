#!/usr/bin/env bash
#
# k8s-e2e.sh — spin up a throwaway kind cluster and run the K8s agent-box
# backend's e2e suite (the reconciler driven against a real apiserver).
#
# Local use:    bash scripts/k8s-e2e.sh
# Keep cluster: E2E_KEEP=1 bash scripts/k8s-e2e.sh   (skips teardown for debugging)
# CI:           invoked by .github/workflows/k8s-e2e.yml on an ubuntu runner.
#
# Requirements: kind, kubectl, go, and a working Docker daemon (all
# preinstalled on the GitHub ubuntu-latest runner). kubectl installs the
# agent-sandbox controller; the e2e itself talks to the apiserver via
# client-go.
#
# CNI: kind's default (kindnet) does NOT enforce NetworkPolicy, so this suite
# used to assert only that the reconciler creates the right objects — the
# policy itself was never exercised. It now builds the cluster with Calico by
# default (E2E_CNI below) so enforcement is actually tested; set
# E2E_CNI=kindnet for a faster local loop, and the enforcement test SKIPS
# rather than passing vacuously (#1234).
set -euo pipefail

CLUSTER="${KIND_CLUSTER:-containarium-k8s-e2e}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

KUBECONFIG_FILE="$(mktemp)"
export KUBECONFIG="$KUBECONFIG_FILE"

cleanup() {
  if [ "${E2E_KEEP:-}" = "1" ]; then
    echo "==> E2E_KEEP=1 — leaving cluster '$CLUSTER' up (KUBECONFIG=$KUBECONFIG_FILE)"
    return
  fi
  echo "==> tearing down kind cluster '$CLUSTER'"
  kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
  rm -f "$KUBECONFIG_FILE"
}
trap cleanup EXIT

# kind's default CNI (kindnet) does NOT enforce NetworkPolicy, so a cluster
# built with it silently passes every isolation assertion — the per-tenant
# default-deny policy is created and then ignored. That is how #1195 (ingress
# restricted to the sshpiper pod, egress to cluster DNS) merged on a green
# e2e that could not have failed. See #1234.
#
# Calico enforces NetworkPolicy, so the isolation cases below are real. Set
# E2E_CNI=kindnet to opt out (faster startup, but NetworkPolicy assertions
# become vacuous — the e2e skips them rather than passing them falsely).
E2E_CNI="${E2E_CNI:-calico}"

echo "==> creating kind cluster '$CLUSTER' (cni=$E2E_CNI)"
if [ "$E2E_CNI" = "calico" ]; then
