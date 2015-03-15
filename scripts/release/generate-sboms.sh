#!/usr/bin/env bash
# Generate an SPDX SBOM for each Linux release binary and list them in
# SHA256SUMS.txt (#2561).
#
# Usage: scripts/release/generate-sboms.sh <release-dir>
#
# <release-dir> is where `make build-release` put the artifacts and wrote
# SHA256SUMS.txt (bin/ in CI). Needs `syft` and `jq`; set SYFT to the syft
# binary's path when it is not on PATH (the CI download action returns a path).
#
# One SBOM per distinct program, on linux/amd64: a Go binary embeds its module
# list, so syft reads the dependency set straight from the shipped file rather
# than from go.mod, and the darwin and windows builds of the same program link
# the same modules. The container images already carry their own SBOM
# (provenance + sbom in their build jobs); the agent-runtime Node bundle does
# not have one yet.
set -euo pipefail

dir="${1:?usage: generate-sboms.sh <release-dir>}"
syft_bin="${SYFT:-syft}"
command -v "$syft_bin" >/dev/null || { echo "syft not found ($syft_bin); set SYFT or put it on PATH" >&2; exit 2; }
