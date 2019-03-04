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
