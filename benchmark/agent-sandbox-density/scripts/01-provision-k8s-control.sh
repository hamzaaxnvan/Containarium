#!/bin/bash
#
# Provision the control-group VM: the shared base cluster (see
# k8s-common.sh — kubeadm, CNI, raised max-pods, gVisor, upstream
# kubernetes-sigs/agent-sandbox controller) with nothing Containarium-
# specific on top. The control group creates `Sandbox` CRs directly via
# kubectl (02); their pods are scheduled under gVisor exactly like the
# experiment group's (manifests/sandbox-template.yaml sets
# runtimeClassName). See README.md "What's actually under test" — gVisor
# is deliberately identical on both sides, so what differs is Containarium
# the orchestrator, not gVisor.
#
# Usage:
