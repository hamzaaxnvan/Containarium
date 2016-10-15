#!/usr/bin/env bash
#
# code-run-fake-agent-e2e.sh — plumbing e2e for `containarium code
# run/status/attach/stop` using a FAKE agent, so it needs no model, no
# Anthropic credits and no `claude setup-token`.
#
# `code run` executes `~/.local/bin/claude -p '<prompt>' [--output-format
# stream-json]` on the box (internal/coderun/ops.go). This script swaps that
# path for a stub whose behaviour is chosen by the prompt, then proves the
# transport around the agent: launch, streaming, quoting, framed capture,
# detach/reattach, stop, and same-box concurrency. It says NOTHING about
# whether a real agent does good work — that is the (blocked) real-agent lane.
#
# Requirements:
#   - a built CLI (default ./bin/containarium; override with CONTAINARIUM_BIN)
#   - a logged-in CLI / CONTAINARIUM_SERVER + token for the target daemon
#   - an EXISTING box you may scribble on: E2E_BOX=<box>
#     (the script backs up and restores any real ~/.local/bin/claude)
#
# Usage:
#   E2E_BOX=<box> bash scripts/code-run-fake-agent-e2e.sh
#
# Prove-it-can-fail: E2E_SABOTAGE=silent-agent makes the stub print nothing,
# so the streaming assertions MUST go red:
#   E2E_SABOTAGE=silent-agent E2E_BOX=<box> bash scripts/code-run-fake-agent-e2e.sh; echo $?  # non-zero
#
# Assertion 5 proves `code status` surfaces a failing agent's exit code
# (#2011), and that `code run` itself exits non-zero when the agent does.

set -uo pipefail

BIN="${CONTAINARIUM_BIN:-./bin/containarium}"
BOX="${E2E_BOX:?set E2E_BOX=<an existing box to run the fake agent on>}"
SABOTAGE="${E2E_SABOTAGE:-}"
RUN="e2e-$$"                       # unique run-name prefix; never collides with "code"
WORK="$(mktemp -d)"
REMOTE_CLAUDE='$HOME/.local/bin/claude'
FAILS=0
