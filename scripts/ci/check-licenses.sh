#!/usr/bin/env bash
# Dependency licence gate (#2561): every Go module linked into a binary we ship
# must carry a licence on the allow-list. Run it locally the same way CI does:
#
#   go install github.com/google/go-licenses@v1.6.0
#   scripts/ci/check-licenses.sh
#
# What is checked: the full transitive package graph of each shipped program
# (the daemon, the platform MCP, agent-box, the model gateway, and the client
# built with -tags containarium_client), not just go.mod's direct requirements.
# The allow-list and the two exceptions are policy; see
# docs/security/DEPENDENCY-LICENSES.md before changing them.
set -euo pipefail

