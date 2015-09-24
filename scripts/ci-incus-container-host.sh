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

