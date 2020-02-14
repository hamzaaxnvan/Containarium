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
PIDS=()

ok()   { echo "OK   $*"; }
fail() { echo "FAIL $*"; FAILS=$((FAILS + 1)); }
box_exec() { "$BIN" connect "$BOX" --exec "$1"; }
code() { "$BIN" code "$1" "$BOX" "${@:2}"; }

assert_contains() { # <desc> <haystack> <needle>
  if grep -qF -- "$3" <<<"$2"; then ok "$1"; else fail "$1 — missing '$3' in: $(head -c 300 <<<"$2")"; fi
}
assert_not_contains() {
  if grep -qF -- "$3" <<<"$2"; then fail "$1 — unexpected '$3'"; else ok "$1"; fi
}
wait_status() { # <name> <regex> <secs>
  local end=$((SECONDS + $3)) out
  while [ $SECONDS -lt $end ]; do
    out="$(code status --name "$1" 2>&1)"
    grep -qE "$2" <<<"$out" && { echo "$out"; return 0; }
    sleep 1
  done
  echo "$out"; return 1
}

cleanup() {
  for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null; done
  for n in "$RUN-long" "$RUN-a" "$RUN-b"; do code stop --name "$n" --force >/dev/null 2>&1; done
  box_exec 'rm -f /tmp/e2e-pwned-*; if [ -f $HOME/.local/bin/claude.e2e-orig ]; then mv -f $HOME/.local/bin/claude.e2e-orig $HOME/.local/bin/claude; else rm -f $HOME/.local/bin/claude; fi' >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

# ---- the fake agent -------------------------------------------------------
# Behaviour by prompt content (argv: -p <prompt> [--output-format stream-json]):
#   contains FAIL   -> exits 1
#   contains SLEEP  -> heartbeat every second for 120 s
#   otherwise       -> echoes the prompt and a marker, exits 0
cat >"$WORK/claude" <<'STUB'
#!/bin/sh
prompt="$2"; fmt="$4"
