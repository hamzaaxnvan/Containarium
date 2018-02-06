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
if [ "$fmt" = stream-json ]; then
  printf '{"type":"system","subtype":"init"}\n'
  echo 'e2e-stderr-diagnostic' >&2
  printf '{"type":"result","subtype":"success"}\n'
else
  printf 'FAKE-AGENT-PROMPT<<%s>>\n' "$prompt"
  echo FAKE-AGENT-DONE
fi
case "$prompt" in
  *FAIL*)  exit 1 ;;
  *SLEEP*) i=0; while [ $i -lt 120 ]; do echo "heartbeat-$i"; i=$((i+1)); sleep 1; done ;;
esac
exit 0
STUB
if [ "$SABOTAGE" = silent-agent ]; then sed -i.bak '1a\
exit 0' "$WORK/claude"; fi

# `connect` authorizes the key via the API, but the sentinel only learns of it
# at its next keysync (~2 min cadence). On a fresh box the first connects fail
# with "Permission denied (publickey)" until then, so wait instead of failing.
echo "== wait for SSH to $BOX (up to ${E2E_SSH_WAIT:-300}s)"
t0=$SECONDS
until "$BIN" connect "$BOX" --exec true >/dev/null 2>&1; do
  [ $((SECONDS - t0)) -ge "${E2E_SSH_WAIT:-300}" ] && { echo "SSH to $BOX not ready after $((SECONDS - t0))s"; exit 2; }
  sleep 10
done
echo "SSH ready after $((SECONDS - t0))s"

# `code run` speaks MCP to `ssh <box> -- agent-box`, so the box needs agent-box
# on PATH (/usr/local/bin). Boxes provisioned for coding agents have it; a plain
# fresh box does not, and every `code` command then dies with "initialize MCP
# session: transport closed". Build it with `make build-agent-box-linux`.
AGENT_BOX_BIN="${E2E_AGENT_BOX_BIN:-./bin/agent-box-linux-amd64}"
if [ -n "${E2E_FORCE_AGENT_BOX_INSTALL:-}" ] || ! box_exec 'command -v agent-box' >/dev/null 2>&1; then
  echo "== install agent-box on $BOX from $AGENT_BOX_BIN"
  [ -f "$AGENT_BOX_BIN" ] || { echo "missing $AGENT_BOX_BIN (make build-agent-box-linux)"; exit 2; }
  SSH_CMD="$("$BIN" connect "$BOX" --print | tail -1)"
  # shellcheck disable=SC2086  # SSH_CMD is a command line, split on purpose
  $SSH_CMD 'sudo tee /usr/local/bin/agent-box >/dev/null && sudo chmod 755 /usr/local/bin/agent-box' <"$AGENT_BOX_BIN" \
