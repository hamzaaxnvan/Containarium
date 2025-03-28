#!/usr/bin/env bash
#
# engineer-crew-e2e.sh — the executable proof that `engineer-crew` turns an
# issue-shaped task into a real change against a real repo, using a REAL model.
#
# Acceptance criterion this script IS (Containarium-cloud#1773, AC2, verbatim):
#
#   OSS `cluster-e2e`: `engineer-crew` against the pinned public fixture repo
#   with a fixture task returns an artifact whose `files` touch a path that
#   exists in the fixture and whose `pr_title` is non-empty; the existing
#   credential-hygiene and workspace-removed assertions still pass.
#
# WHY THIS SCRIPT EXISTS AT ALL, given engineer-crew already has a unit test.
# internal/server/crew_server_test.go's TestEmbeddedEngineerCrewValidatesAndDrives
# drives the same crew with a FAKE model backend: it hands driveCrew canned
# artifact JSON and proves the PIPELINE hand-off preserves
# pr_title/pr_body/tests_run. That is the right test for the hand-off, and it
# is structurally incapable of proving the thing AC2 asks about — that a real
# model, given issue-implementer's prompt and a real checkout, produces
# well-formed artifact JSON naming a file that really exists. The only way to
# learn that is to run the model. So this lane spends real provider money, and
# is the ONLY lane in this repo that does.
#
# THIS IS THE FIRST LANE IN THE REPO THAT SPENDS REAL MODEL MONEY — read the
# "credential contract" and "cost" sections below before wiring it anywhere.
#
# --- RELATIONSHIP TO scripts/agent-skill-lease-e2e.sh --------------------
# That script is this one's direct ancestor and most of the shape here is
# lifted from it deliberately (real daemon, real Postgres, real Incus box,
# the same octocat/Spoon-Knife pinned fixture, the same fail-loudly-never-
# skip-silently posture). Two things differ, and both matter:
#
#   1. It uses a PLACEHOLDER provider key ("not-a-real-key-e2e-placeholder")
#      on purpose: its assertions only ever distinguish "the gateway rejected
#      this credential" from "the gateway accepted it and went upstream", so
#      whatever the upstream says is equally good evidence. No model ever
#      runs there. This script needs the opposite — the model's OUTPUT is the
#      thing under test — so it needs a real key and it declines to run
#      without one.
#   2. It exercises RunAgentSkill (one skill, one box, lease ended on exit).
#      This exercises RunCrew (two skills, two boxes, serve mode) — but RunCrew
#      now ends every member's lease at each of its three terminal paths too
#      (#2100/#2101), so the "workspace-removed" half of AC2 holds the same
#      way it does there; see assertion 7.
#
# --- WHAT IT ASSERTS, in order (each prints OK with what it observed) ----
#
#   1. real model reachable   — the gateway is mounted with a REAL provider key
#                               and a per-tenant spend ceiling; a member box's
#                               own gateway token is ACCEPTED mid-run.
#   2. run COMPLETED          — RunCrew returns 200 with
#                               CREW_RUN_STATE_COMPLETED and a non-empty
#                               artifact_json.
#   3. artifact well-formed   — artifact_json parses and carries a non-empty
#                               `files` array (AC2's first half).
#   4. files touch the fixture— at least one `files[].path` exists in the
#                               fixture at the PINNED commit, checked against
#                               a file list this script computes itself from
#                               an independent clone (AC2's first half).
#   5. pr_title non-empty     — AC2's second half.
#   6. credential hygiene     — the REAL provider key appears nowhere: not in
#                               the artifact, not in the RPC response, not in
#                               the daemon log, not in either box's seed
#                               directory. The box only ever held a scoped
#                               gateway token (the model-gateway's whole
#                               point); and a supplied git_credential leaks
#                               nowhere either.
#   7. workspace state        — the fetched workspace was the PINNED commit
#                               (CrewRun.git_commit), and every member's
#                               per-run seed directory and workspace are gone
#                               entirely after the run returns — the same
#                               contract RunAgentSkill's lease-end already has
#                               (#2100/#2101); see assertion 7.
#
# --- CREDENTIAL CONTRACT (how to make the real half actually run) -------
# This script reads the SAME provider-key env vars the daemon itself reads
# (internal/server/agent_gateway.go gatewayProviderKeysFromEnv), in the SAME
# precedence the daemon uses to choose which provider to provision boxes for
# (gatewayPrimaryProvider):
#
#     ANTHROPIC_API_KEY   (engine: claude)
#     OPENAI_API_KEY      (engine: codex)
#     GEMINI_API_KEY      (engine: gemini — "the cheap test engine",
#                          internal/modelgateway/providers.go)
#
# Deliberately NOT a new bespoke env var. Reusing the daemon's own names means
# the key this script hands the daemon is the key the daemon would have used
# anyway, the engine the box runs is the one engineForProvider() picks from it,
# and there is no second place for the two to disagree.
#
# NONE SET ⇒ THIS SCRIPT SKIPS, exit 0, printing what a human must set. That
# is the design's "skipped when no model is configured" and it is checked
# FIRST, before Incus, Postgres, sudo or anything else, so the skip path runs
# on a bare runner in about a second — which is what makes it affordable on
# every pull request (see .github/workflows/cluster-e2e.yml, job
# `engineer-crew-skip-gate`).
#
# A skip is NOT a pass. It prints SKIP, never "PASS", so a green job that
# skipped cannot be misread as evidence the crew works.
#
# --- COST, and what bounds it -------------------------------------------
# ONE crew run: two members, each one model conversation, each capped at
# CONTAINARIUM_AGENT_MAX_TURNS turns. Three independent ceilings, so a runaway
# agent cannot turn a test into an invoice:
#
#   * the daemon's OWN model gateway quota (CONTAINARIUM_GATEWAY_QUOTA_*,
#     internal/config/gateway.go) — set below, per tenant per window. This is
#     the product's own enforcement path, so the lane is also a live exercise
#     of it.
#   * CONTAINARIUM_AGENT_MAX_TURNS, pushed into each box, capping the agent
#     loop.
#   * CONTAINARIUM_E2E_ENGINEER_CREW_MODEL, pushed into each box, so the lane
#     does not silently run on agent-runtime's default (claude-opus-4-8) when
#     a cheap model proves the same property.
#
# The boxes are NOT pre-provisioned by a warm-up model run, deliberately: this
# script deploys the agent-runtime recipe for each member box directly (the
# same box names provisionSkillBox would use, so its idempotent reuse path
# picks them up) and verifies `agent-runtime` is actually on PATH in each
# BEFORE spending anything. A missing in-box runtime would otherwise surface
# as an obscure A2A failure after the money was already committed.
#
# --- Local use -----------------------------------------------------------
#   # skip path (no spend), proves the gate:
#   bash scripts/engineer-crew-e2e.sh
#
#   # real path:
#   sudo -v && ANTHROPIC_API_KEY=sk-... bash scripts/engineer-crew-e2e.sh
#
# --- Prove-it-can-fail (the #1418 guardrail this repo applies to every e2e
# lane; a green lane that has never failed proves nothing) ----------------
# Both sabotages corrupt the HARNESS's own view after a real run rather than
# trying to make a model misbehave on demand, because a model cannot be
# reliably instructed to produce a wrong answer and a lane's guardrail must be
# deterministic. Each costs one real run, so both are workflow_dispatch-only.
#
#   CONTAINARIUM_E2E_SABOTAGE=fixture-path-mismatch
# Replaces the independently-computed fixture file list with paths the fixture
# does not contain, so assertion 4 ("files touch the fixture") must go RED.
# Proves AC2's first half discriminates — that it is checking the artifact
# against the real tree and not against itself.
#
#   CONTAINARIUM_E2E_SABOTAGE=blank-pr-title
# Blanks pr_title in the artifact the assertions read, so assertion 5 must go
# RED. Proves AC2's second half is a gate rather than a print statement.
#
# --- Host requirements (the real path only; the skip path needs none) ----
#   - Incus with a usable storage pool at /var/lib/incus/unix.socket. No KVM:
#     a skill box is an Incus system container (the agent-skill-lease-e2e.sh /
#     cluster-container-e2e.yml precedent).
#   - Go toolchain, passwordless sudo, curl, jq, git
#   - Postgres via CONTAINARIUM_POSTGRES_URL, or docker/podman so this script
#     can start a throwaway one.
#   - Egress to github.com (the fixture, and the agent-runtime release
#     artifacts the box's post_start pulls) AND to the model provider. Unlike
#     the lease lane, provider egress IS required here.
set -euo pipefail

# ========================================================================
# THE SKIP GATE. First, before everything — see "credential contract".
# ========================================================================

# Provider precedence must match internal/server/agent_gateway.go's
# gatewayPrimaryProvider(), because that is what decides which provider the
# daemon provisions boxes for. If these two ever disagree, this script hands
# the daemon one key and then asserts against a box wired for another.
PROVIDER=""
PROVIDER_KEY=""
PROVIDER_KEY_ENV=""
for cand in "anthropic:ANTHROPIC_API_KEY" "openai:OPENAI_API_KEY" "gemini:GEMINI_API_KEY"; do
  cand_provider="${cand%%:*}"
  cand_env="${cand##*:}"
  cand_val="${!cand_env:-}"
  if [ -n "$cand_val" ]; then
    PROVIDER="$cand_provider"
    PROVIDER_KEY="$cand_val"
    PROVIDER_KEY_ENV="$cand_env"
    break
  fi
done

if [ -z "$PROVIDER_KEY" ]; then
  cat <<'SKIPEOF'
SKIP: engineer-crew e2e needs a REAL model credential and none is configured.

This lane is the one place in this repo where a model actually runs: AC2 of
Containarium-cloud#1773 asks whether engineer-crew's OUTPUT is well-formed and
names a real file, and no fake backend can answer that. So it declines to run
rather than pretend.

To run it, set exactly one of these — the same variables the daemon's own model
gateway reads (internal/server/agent_gateway.go gatewayProviderKeysFromEnv),
in the daemon's own precedence:

    ANTHROPIC_API_KEY   -> engine claude
    OPENAI_API_KEY      -> engine codex
    GEMINI_API_KEY      -> engine gemini  (the cheap test engine)

In CI: add the corresponding Actions secret to this repository and let
.github/workflows/cluster-e2e.yml's `engineer-crew-live` job pick it up. As of
this script landing, that secret does NOT exist yet — a human with repository
admin has to create it, and until then the real half of this lane has never
run. Nothing else is missing.

This is a SKIP, not a PASS. Nothing about engineer-crew was proved here.
SKIPEOF
  exit 0
fi

# A placeholder is worse than nothing: it would let the lane march past the
# skip gate and then fail somewhere deep with a provider auth error that reads
# like a daemon bug. The lease lane's own dummy key is the one string most
# likely to be copied in here by accident, so name it explicitly.
case "$PROVIDER_KEY" in
  not-a-real-key-e2e-placeholder|*e2e-placeholder*|*not-a-real*)
    echo "FATAL: $PROVIDER_KEY_ENV looks like a placeholder, not a real provider key." >&2
    echo "       This lane needs a key the provider will actually accept. scripts/agent-skill-lease-e2e.sh" >&2
    echo "       is the lane that runs on a placeholder; this one cannot." >&2
    exit 1 ;;
esac

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
# shellcheck source=/dev/null
. "$REPO_ROOT/scripts/lib/wait-daemon-ready.sh"

# ========================================================================
# Configuration
# ========================================================================

# Ports off cluster-e2e.sh's (15051/18080) and agent-skill-lease-e2e.sh's
# (15071/18090) so all three lanes can share a runner without fighting over a
# listener.
GRPC_PORT="${CONTAINARIUM_E2E_CREW_GRPC_PORT:-15081}"
HTTP_PORT="${CONTAINARIUM_E2E_CREW_HTTP_PORT:-18100}"

CREW_ID="${CONTAINARIUM_E2E_CREW_ID:-engineer-crew}"
# engineer-crew's members, in pipeline order (pkg/core/crews/crews.yaml). Kept
# here rather than read from the catalog because the assertions below name the
# boxes, and a member list that silently grew should fail this lane loudly
# rather than have it check fewer boxes than the crew has.
CREW_MEMBERS="issue-implementer diff-reviewer"
# The crew's pack_producer (#2799). Not an agent: it is provisioned by the
# crew run itself with no agent runtime check, and makes no model call.
PACK_PRODUCER="test-runner"

# The pinned public fixture, same as scripts/agent-skill-lease-e2e.sh: GitHub's
# own decade-old fork-tutorial repo, small, public, and untouched since 2014,
# so pinning its tip is as close to "will never change" as a third party's repo
# gets. Reused rather than re-chosen — one fixture, one pin to keep correct.
FIXTURE_GIT_SOURCE="${CONTAINARIUM_E2E_CREW_FIXTURE_REPO:-https://github.com/octocat/Spoon-Knife}"
FIXTURE_GIT_SHA="${CONTAINARIUM_E2E_CREW_FIXTURE_SHA:-d0dd1f61b33d64e29d8bc1372a94ef6a2fee76a9}"

# The release whose published agent-runtime bundle the member boxes install.
# provisionSkillBox passes the DAEMON'S OWN version as the recipe's `release`
# param (agentRuntimeReleaseTag), and a locally built daemon has a dev version
# whose artifacts were never published — so assembly would silently skip and
# the boxes would have no agent-runtime at all. Pinning a published tag here,
# and stamping the daemon with it at build time, is what makes the in-box loop
# real.
RELEASE="${CONTAINARIUM_E2E_CREW_RELEASE:-$(git tag --list 'v*' --sort=-v:refname | head -1)}"

# Cost ceilings. See the header's "COST" section: three independent ones.
AGENT_MAX_TURNS="${CONTAINARIUM_E2E_CREW_MAX_TURNS:-12}"
# Empty ⇒ the box's own engine default (agent-runtime/src/index.ts). Set a
# cheap model per engine by default: this lane proves the artifact's SHAPE and
# that it names a real file, which does not need a frontier model.
case "$PROVIDER" in
  anthropic) AGENT_MODEL_DEFAULT="claude-3-5-haiku-latest" ;;
  gemini)    AGENT_MODEL_DEFAULT="gemini-2.5-flash" ;;
  *)         AGENT_MODEL_DEFAULT="" ;;
esac
AGENT_MODEL="${CONTAINARIUM_E2E_CREW_MODEL-$AGENT_MODEL_DEFAULT}"

# The daemon's own model-gateway quota (internal/config/gateway.go). Per tenant
# per window; a crew member is its own tenant ("agent-<skill>"), so each member
# gets this ceiling. Generous enough that a legitimate run never trips it,
# small enough that a loop cannot bill.
GATEWAY_QUOTA_WINDOW="${CONTAINARIUM_E2E_CREW_QUOTA_WINDOW:-30m}"
GATEWAY_QUOTA_CALLS="${CONTAINARIUM_E2E_CREW_QUOTA_CALLS:-80}"
GATEWAY_QUOTA_TOKENS="${CONTAINARIUM_E2E_CREW_QUOTA_TOKENS:-400000}"

# How long to wait for the crew run. A real two-member model run is minutes,
# and the first box provision (the recipe's post_start installs Node) is on top
# of that — but that happens in the pre-provision step below, not here.
RUN_TIMEOUT_S="${CONTAINARIUM_E2E_CREW_RUN_TIMEOUT:-1800}"

SABOTAGE="${CONTAINARIUM_E2E_SABOTAGE:-}"

# A fake bearer token for the fixture fetch. Obviously fake and never a real
# secret; its only job is to be a string that must appear nowhere. Unlike
# agent-skill-lease-e2e.sh's assertion 8, this one is NOT passed on the measured
# run — GitHub rejects any invalid Authorization header outright, even for a
# public repo, so attaching it would break the fetch the whole lane depends on.
# It is passed on a separate throwaway run at the end.
FIXTURE_GIT_CREDENTIAL="e2e-fake-credential-1773-$$-not-a-real-secret"

# Matches the daemon's agentSeedRoot / agentWorkspaceRoot
# (internal/server/agent_server.go).
