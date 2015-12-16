#!/usr/bin/env bash
#
# two-engine-crew-e2e.sh — the executable proof of the agent-router PRD's
# Story 4 and north-star metric (share of runs whose recorded engine matches
# the manifest: 100%): several agent engines running on ONE daemon at once,
# each skill routed to the engine its own manifest names (#2222/#2223).
#
# WHAT THIS SCRIPT IS, AND ISN'T. It runs the two-engine-crew fixture
# (pkg/core/crews/crews.yaml): hello-agent-claude (engine: claude) then
# hello-agent-codex (engine: codex), pipeline topology, against ONE daemon
# holding BOTH ANTHROPIC_API_KEY and OPENAI_API_KEY but deliberately NOT
# GEMINI_API_KEY — so the same run also proves the NOT_READY half of the
# readiness report (#2223) against a provider this daemon genuinely lacks.
#
# --- WHAT IT ASSERTS, in order (each prints OK with what it observed) ----
#
#   1. engine readiness report  — BEFORE any spend: `containarium agent
#                                  engines --json` on this daemon reports
#                                  claude READY, codex READY, gemini
#                                  NOT_READY, and the assertion is on the
#                                  `reason` string (#2225 AC2), not just the
#                                  readiness enum.
#   2. member gateway tokens    — each member box's own gateway.env token
#                                  (minted when RunCrew provisions it, before
#                                  driveCrew runs a single hop) decodes to a
#                                  DIFFERENT `provider` claim: hello-agent-
#                                  claude's -> anthropic, hello-agent-codex's
#                                  -> openai. This is the per-member proof
#                                  that #2225 AC1 asks for.
#   3. run COMPLETED             — RunCrew returns 200 with
#                                  CREW_RUN_STATE_COMPLETED.
#
# --- WHY NOT "EACH MEMBER'S ARTIFACT.JSON RECORDS ITS OWN ENGINE" (#2225's
# literal AC1 wording) -----------------------------------------------------
# artifact.json (agent-runtime/src/artifact.ts, written by run.ts's ONE-SHOT
# `runOnce()`) is real, and DOES carry an `engine` field — but a crew hop
# never goes through that path. RunCrew's provisionMemberBox starts each
# member in SERVE mode (CONTAINARIUM_AGENT_MODE=serve), whose A2A handler
# (agent-runtime/src/a2a.ts runTask) returns the artifact straight over HTTP
# and never calls writeArtifact. Nor does the daemon's own runlease registry
# help: internal/runlease/registry.go keys Info by run_id ALONE, and both
# crew members share one run_id, so the second member's Register() call
# overwrites the first's — there is no way to read back member 1's recorded
# engine once member 2 has registered.
#
# Decision (interactive, 2026-10-02, on #2225): assert per-member engine via
# the gateway token's `provider` claim instead (assertion 2 above, already
# named in the issue's AC1 as the second check) plus the readiness report
# (assertion 1). No product code changed to make the literal artifact.json
# wording true — that would be new engine-serve-mode or registry behavior,
# out of scope for a test script. If a future issue wants per-member engine
# observable from artifact.json on the crew path too, it is product work for
# agent-runtime / internal/runlease, not this script.
#
# --- CREDENTIAL CONTRACT ---------------------------------------------------
# Reads the SAME provider-key env vars the daemon itself reads
# (internal/server/agent_gateway.go gatewayProviderKeysFromEnv):
#
#     ANTHROPIC_API_KEY   (engine: claude)
#     OPENAI_API_KEY      (engine: codex)
#
# BOTH must be set and look like real keys, or this script SKIPS, exit 0,
# naming whichever is missing. Per the sprint's Q5 decision on #2225/#2230:
# the live CI job and the dev environment are NOT assumed to hold both —
# this is a skip, never a silent pass on one key. GEMINI_API_KEY must stay
# UNSET for this run: assertion 1 needs a provider this daemon genuinely has
# no key for, to prove the NOT_READY half of the report. If it is set in
# your shell, unset it before running this script.
#
# A skip is NOT a pass. It prints SKIP, never "PASS".
#
# --- COST ------------------------------------------------------------------
# ONE crew run, two tiny hello-agent turns (not engineer-crew's real
# issue-sized task): CONTAINARIUM_AGENT_MAX_TURNS caps each member's loop,
# and the claude member's model is pinned to a cheap one
# (claude-3-5-haiku-latest) via the same /etc/profile.d seam
# scripts/engineer-crew-e2e.sh uses; the codex member's model is left unset
# (its own CLI default decides — codex has no hard-coded default in
# agent-runtime, see agent-runtime/src/index.ts).
#
# --- Local use --------------------------------------------------------------
#   # skip path (no spend), proves the gate:
#   bash scripts/two-engine-crew-e2e.sh
#
#   # real path (needs sudo for Incus and both provider keys):
#   sudo -v && unset GEMINI_API_KEY && \
#     ANTHROPIC_API_KEY=sk-... OPENAI_API_KEY=sk-... bash scripts/two-engine-crew-e2e.sh
#
# --- Prove-it-can-fail (the #1418 guardrail every e2e lane in this repo
# applies; a green lane that has never failed proves nothing) -------------
#   CONTAINARIUM_E2E_SABOTAGE=swap-expected-providers
# Swaps which provider the harness EXPECTS from which member right before
# assertion 2 compares them, so the real tokens (correct, unchanged) now
# read as wrong — assertion 2 must go RED. Proves AC1's per-member check
# discriminates: it corrupts the HARNESS's own expectation, exactly the
# shape an implementer's real mistake (wiring a member to the wrong engine)
# would produce, rather than trying to make a model misbehave on demand.
# Costs one real run, so this is workflow_dispatch-only (see the workflow).
#
# --- Host requirements (the real path only; the skip path needs none) ----
#   - Incus with a usable storage pool at /var/lib/incus/unix.socket.
#   - Go toolchain, passwordless sudo, curl, jq, git.
#   - Postgres via CONTAINARIUM_POSTGRES_URL, or docker/podman so this script
#     can start a throwaway one.
#   - Egress to github.com (the agent-runtime release artifacts the box's
#     post_start pulls) AND to both model providers.
set -euo pipefail

# ========================================================================
# THE SKIP GATE. First, before everything — see "credential contract".
# ========================================================================
missing=""
[ -n "${ANTHROPIC_API_KEY:-}" ] || missing="$missing ANTHROPIC_API_KEY"
[ -n "${OPENAI_API_KEY:-}" ] || missing="$missing OPENAI_API_KEY"

if [ -n "$missing" ]; then
  cat <<SKIPEOF
SKIP: two-engine-crew e2e needs REAL claude AND codex credentials; missing:$missing

This lane proves #2225 (agent-router PRD Story 4): several engines on one
daemon at once. Half the point is BOTH providers configured simultaneously,
so it declines to run on just one — that would only re-prove what
scripts/engineer-crew-e2e.sh already proves for a single engine.

To run it, set BOTH of these (the same vars the daemon's own model gateway
reads, internal/server/agent_gateway.go gatewayProviderKeysFromEnv):

    ANTHROPIC_API_KEY   -> engine claude
    OPENAI_API_KEY      -> engine codex

In CI: this needs an OPENAI_API_KEY secret that, as of this script landing,
does NOT exist on this repository (only ANTHROPIC_API_KEY / GEMINI_API_KEY
do, for scripts/engineer-crew-e2e.sh) — a human with repository admin has to
add it. Until then the real half of this lane has never run; the scaffolding
is green (see two-engine-crew-skip-gate on every PR).

This is a SKIP, not a PASS. Nothing about engine routing was proved here.
SKIPEOF
  exit 0
fi

for cand in "ANTHROPIC_API_KEY:$ANTHROPIC_API_KEY" "OPENAI_API_KEY:$OPENAI_API_KEY"; do
  cand_env="${cand%%:*}"
  cand_val="${cand#*:}"
  case "$cand_val" in
    not-a-real-key-e2e-placeholder|*e2e-placeholder*|*not-a-real*)
      echo "FATAL: $cand_env looks like a placeholder, not a real provider key." >&2
      echo "       This lane needs keys the providers will actually accept." >&2
      exit 1 ;;
  esac
done

if [ -n "${GEMINI_API_KEY:-}" ]; then
  echo "FATAL: GEMINI_API_KEY is set, but assertion 1 needs gemini to be a provider this daemon has NO key for." >&2
  echo "       Unset it before running this script." >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
# shellcheck source=/dev/null
. "$REPO_ROOT/scripts/lib/wait-daemon-ready.sh"

# ========================================================================
# Configuration
# ========================================================================

# Ports off every other e2e lane's (cluster-e2e 15051/18080, agent-skill-
# lease 15071/18090, engineer-crew 15081/18100) so all four can share a
# runner without fighting over a listener.
GRPC_PORT="${CONTAINARIUM_E2E_TWOENGINE_GRPC_PORT:-15091}"
HTTP_PORT="${CONTAINARIUM_E2E_TWOENGINE_HTTP_PORT:-18110}"

CREW_ID="${CONTAINARIUM_E2E_TWOENGINE_CREW_ID:-two-engine-crew}"
# Members in pipeline order (pkg/core/crews/crews.yaml), engine each one
# NAMES on its manifest (pkg/core/skills/skills.yaml) and the provider that
# engine resolves to (internal/agentengine.Provider). Kept here, not read
# from the catalog, so a manifest that silently drops its `engine:` field
# fails this lane loudly instead of quietly checking fewer engines than #2225
# asks for.
declare -A MEMBER_ENGINE=( [hello-agent-claude]=claude [hello-agent-codex]=codex )
declare -A MEMBER_PROVIDER=( [hello-agent-claude]=anthropic [hello-agent-codex]=openai )
CREW_MEMBERS="hello-agent-claude hello-agent-codex"

# The release whose published agent-runtime bundle the member boxes install —
# same reasoning as scripts/engineer-crew-e2e.sh: a locally built daemon has a
# dev version whose artifacts were never published, so this must be a real
# published tag.
RELEASE="${CONTAINARIUM_E2E_TWOENGINE_RELEASE:-$(git tag --list 'v*' --sort=-v:refname | head -1)}"

# Cost ceilings. hello-agent's task is tiny ("do the smallest useful thing"),
# so a low turn cap is correct here, not just cheap.
AGENT_MAX_TURNS="${CONTAINARIUM_E2E_TWOENGINE_MAX_TURNS:-6}"
# claude's engine default (claude-opus-4-8, agent-runtime/src/index.ts) is a
# frontier model this lane does not need; codex's default is already empty
# (its own CLI default decides) so it is left alone.
CLAUDE_MODEL="${CONTAINARIUM_E2E_TWOENGINE_CLAUDE_MODEL:-claude-3-5-haiku-latest}"

RUN_TIMEOUT_S="${CONTAINARIUM_E2E_TWOENGINE_RUN_TIMEOUT:-600}"

SABOTAGE="${CONTAINARIUM_E2E_SABOTAGE:-}"

# Matches the daemon's agentSeedRoot (internal/server/agent_server.go).
AGENT_SEED_ROOT="/etc/containarium/agent/runs"

WORKDIR="$(mktemp -d)"
BIN="$WORKDIR/containariumd"
DAEMON_LOG="$WORKDIR/daemon.log"
PG_CONTAINER=""
CONTAINER_RUNTIME=""
DAEMON_PID=""

log() { echo "==> $*"; }
ok() { echo "OK   $*"; }
fail() { echo "FATAL: $*" >&2; exit 1; }

# ========================================================================
# Pre-flight: fail loudly, never skip silently. (The ONE legitimate skip is
# the credential gate above, and it already happened.)
