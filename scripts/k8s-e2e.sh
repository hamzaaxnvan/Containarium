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

