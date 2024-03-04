#!/usr/bin/env bash
#
# coding-agent-recipe-e2e.sh — proof that the `coding-agent` recipe yields a box
# with the toolchain and NO credential (#2031). UNVERIFIED in CI: it needs a
# running daemon with Incus and egress to GitHub and claude.ai, so it is not yet
# wired into a workflow. Run it by hand against a scratch daemon.
#
# Usage: RELEASE=v0.89.0 bash scripts/coding-agent-recipe-e2e.sh
# Env:   RELEASE (required, v-prefixed), BOX (default coding-agent-e2e),
#        BOX_USER (default ubuntu), CONTAINARIUM (CLI, default containarium)
#        ANTHROPIC_TEST_KEY (optional) — if set, the tester's own key is placed
#        in the box user's ~/.claude/settings.json and `code run` is exercised.
set -euo pipefail

RELEASE="${RELEASE:?set RELEASE to a v-prefixed release tag}"
BOX="${BOX:-coding-agent-e2e}"
BOX_USER="${BOX_USER:-ubuntu}"
CLI="${CONTAINARIUM:-containarium}"

