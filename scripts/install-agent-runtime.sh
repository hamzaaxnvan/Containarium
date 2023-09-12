#!/usr/bin/env bash
# install-agent-runtime.sh — assemble the agent-runtime box (Phase 4a/4b/4c).
#
# Runs INSIDE an agent-runtime LXC. Installs the pieces the in-box loop needs
# so the daemon's `agent-runtime` exec (run mode) and serve mode work:
#   1. agent-box  (Go binary, the in-box MCP tool surface)
#   2. mcp-server (Go binary, the platform MCP — spawned by agent-runtime for a
#      run bound to a tracker connection; #1922 D4). Best-effort, see below.
#   3. agent-runtime (the Node loop component) + a PATH launcher
# Node itself is installed by the recipe's post_start (Node 20).
#
# Artifacts are pulled from a GitHub release:
#   <base>/agent-box-linux-amd64
#   <base>/mcp-server-linux-amd64
#   <base>/agent-runtime-bundle.tar.gz
# where <base> = https://github.com/<REPO>/releases/download/<RELEASE>.
# Built by `make build-agent-box-all` + `make bundle-agent-runtime`.
#
# Flags:
#   --no-agent-runtime  install agent-box + mcp-server only; skip the Node
#                       bundle, npm ci, and the agent-runtime launcher (for
#                       boxes that run a coding agent directly, e.g. the
#                       `coding-agent` recipe, and need no Node loop).
#
# Env:
#   REPO     default FootprintAI/Containarium
#   RELEASE  required — the release tag to pull (e.g. v0.27.0)
#   ARTIFACT_BASE_URL  optional — overrides the computed release base URL
set -euo pipefail

INSTALL_AGENT_RUNTIME=1
for arg in "$@"; do
  case "$arg" in
    --no-agent-runtime) INSTALL_AGENT_RUNTIME=0 ;;
    *)
      echo "install-agent-runtime: unknown argument: $arg" >&2
      exit 2
      ;;
  esac
done

if [ "$INSTALL_AGENT_RUNTIME" -eq 1 ]; then
    command -v flock >/dev/null || { echo "install-agent-runtime: flock is required" >&2; exit 1; }
fi

REPO="${REPO:-FootprintAI/Containarium}"
RELEASE="${RELEASE:-}"
if [[ -z "${ARTIFACT_BASE_URL:-}" ]]; then
  if [[ -z "$RELEASE" ]]; then
    echo "install-agent-runtime: set RELEASE (the release tag) or ARTIFACT_BASE_URL" >&2
    exit 2
  fi
  ARTIFACT_BASE_URL="https://github.com/${REPO}/releases/download/${RELEASE}"
