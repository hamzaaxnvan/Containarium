#!/usr/bin/env bash
# verify-agent-runtime-bundle.sh — guard the release bundle's contract (#748).
#
# Asserts the packaged agent-runtime-bundle ships:
#   1. a compiled dist/engines/<name>.js for EVERY engine in src/engines/<name>.ts
#      (so an engine can't silently drop from a release — e.g. gemini was missing
#      from v0.33.0 because that tag predated the engine; this fails the build if
#      tsc/packaging ever skips one), and
#   2. each runtime SDK as a package.json *dependency* (not devDependency), so the
#      box's `npm ci --omit=dev` actually installs it.
#
# Run by `make bundle-agent-runtime` after packaging, and in the release workflow.
#
# Usage: verify-agent-runtime-bundle.sh <bundle.tar.gz> [src-engines-dir]
set -euo pipefail

BUNDLE="${1:?usage: verify-agent-runtime-bundle.sh <bundle.tar.gz> [src-engines-dir]}"
SRC_ENGINES_DIR="${2:-agent-runtime/src/engines}"
# Runtime SDKs the engines import; each must be a (non-dev) dependency.
REQUIRED_DEPS=(@anthropic-ai/claude-agent-sdk @openai/codex-sdk @google/genai @modelcontextprotocol/sdk)

[ -f "$BUNDLE" ] || { echo "verify-bundle: no such bundle: $BUNDLE" >&2; exit 2; }
