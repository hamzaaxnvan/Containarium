#!/usr/bin/env bash
# Daemon-artifact-name gate for the CLI client/server split, Rollout
# Phase 2 (#1784, docs/architecture/cli-client-server-split.md).
#
# From Phase 2 on, the containarium-<os>-<arch> release artifacts are the
# CLIENT build. A host-side installer, provisioner, bundle script or
# terraform file that still downloads (or copies out of bin/) a
# containarium-linux-* / containarium-${OS}-* artifact and installs it as
# the daemon would put the client where containariumd belongs — the unit
# then fails at start with the moved-command stub. Every such consumer
# must name containariumd-<os>-<arch> instead.
#
# This is the release-artifact companion of test-old-binary-path-gate.sh
# (#1781), which covers the on-host INSTALL path; this covers the SOURCE
# artifact name. Go code is pinned separately by
# internal/sentinel/artifact_name_test.go (releaseBinaryName).
#
# .github/workflows/ is not scanned: release.yml legitimately lists
# bin/containarium-* as upload files (the client artifacts themselves).
set -uo pipefail
cd "$(dirname "$0")/.."

# containarium-linux-…  or  containarium-${OS}-… / containarium-$OS-…
# (the trailing "-" after containarium excludes containariumd-*).
PATTERN='containarium-(linux-|\$\{?OS\}?-)'

# Files whose remaining hits are NOT release-artifact consumers.
ALLOWLIST=(
  # The client-only installer (laptops, CI runners): containarium-* is
  # exactly the artifact it should fetch.
  "hacks/install-cli.sh"
)

