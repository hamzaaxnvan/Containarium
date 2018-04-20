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
AGENT_SEED_ROOT="/etc/containarium/agent/runs"
AGENT_WORKSPACE_ROOT="/workspace/runs"

WORKDIR="$(mktemp -d)"
BIN="$WORKDIR/containariumd"
DAEMON_LOG="$WORKDIR/daemon.log"
FIXTURE_CLONE="$WORKDIR/fixture-clone"
PG_CONTAINER=""
CONTAINER_RUNTIME=""
DAEMON_PID=""

log() { echo "==> $*"; }
ok() { echo "OK   $*"; }
fail() { echo "FATAL: $*" >&2; exit 1; }

# ========================================================================
# Pre-flight: fail loudly, never skip silently. (The ONE legitimate skip is
# the credential gate above, and it already happened.)
# ========================================================================
[ -S /var/lib/incus/unix.socket ] || fail "no Incus socket at /var/lib/incus/unix.socket"
command -v go >/dev/null || fail "no Go toolchain"
command -v curl >/dev/null || fail "no curl"
command -v git >/dev/null || fail "no git (this script computes the fixture's file list from its own clone)"
command -v jq >/dev/null || fail "no jq (the RPC response is JSON and this script asserts on its fields)"
sudo -n true 2>/dev/null || fail "needs passwordless sudo (daemon and Incus operations run as root)"
[ -n "$RELEASE" ] || fail "no release tag: set CONTAINARIUM_E2E_CREW_RELEASE to a PUBLISHED v-tag whose agent-runtime bundle exists, or fetch tags so 'git tag --list v*' is non-empty"
case "$RELEASE" in v*) ;; *) fail "CONTAINARIUM_E2E_CREW_RELEASE='$RELEASE' must be v-prefixed (install-agent-runtime.sh builds the artifact URL from it)" ;; esac
case "$SABOTAGE" in
  ''|fixture-path-mismatch|blank-pr-title) ;;
  *) fail "unknown CONTAINARIUM_E2E_SABOTAGE=$SABOTAGE (want empty, 'fixture-path-mismatch' or 'blank-pr-title')" ;;
esac

cleanup() {
  status=$?
  set +e
  if [ -n "$DAEMON_PID" ]; then
    log "stopping daemon (pid $DAEMON_PID)"
    sudo kill "$DAEMON_PID" 2>/dev/null
    sleep 2
    sudo kill -9 "$DAEMON_PID" 2>/dev/null
  fi
  # Member boxes are named deterministically from the skill id, so a red run
  # would otherwise hand its boxes — including a serve-mode agent-runtime
  # holding a stale gateway token — to the next run.
  for m in $CREW_MEMBERS $PACK_PRODUCER; do
    if sudo incus info "agent-$m-container" >/dev/null 2>&1; then
      log "sweeping member box agent-$m-container"
      sudo incus delete --force "agent-$m-container" 2>/dev/null
    fi
  done
  if [ -n "$PG_CONTAINER" ]; then
    log "stopping throwaway postgres"
    "$CONTAINER_RUNTIME" rm -f "$PG_CONTAINER" >/dev/null 2>&1
  fi
  if [ $status -ne 0 ] && [ -f "$DAEMON_LOG" ]; then
    # Gateway and crew lines over the WHOLE log first: whether the gateway was
    # mounted at all, whether a member's serve mode started, and whether a hop
    # failed are each decided in one line the daemon prints once, and a tail
    # alone cannot answer them.
    echo "---- daemon log: gateway / crew / agent-skill lines (whole run) ----"
    grep -E 'model-gateway|Model-gateway|\[crew\]|\[agent-skill\]|quota' "$DAEMON_LOG" || echo "(none)"
    echo "---- daemon log (last 120 lines) ----"
    tail -120 "$DAEMON_LOG"
    # The in-box loop's own log is where a model/tool failure actually lands;
    # the daemon only ever sees "the hop failed".
    for m in $CREW_MEMBERS; do
      echo "---- agent-runtime.log in agent-$m-container (last 60 lines) ----"
      sudo incus exec "agent-$m-container" -- tail -60 /var/log/agent-runtime.log 2>/dev/null || echo "(unavailable)"
    done
  fi
  sudo rm -rf "$WORKDIR" 2>/dev/null || rm -rf "$WORKDIR"
  exit $status
}
trap cleanup EXIT

# The release's agent-runtime bundle must actually be published, checked BEFORE
# any spend. install-agent-runtime.sh's failure is best-effort by design (the
# recipe logs "assembly skipped/failed" and carries on), so a wrong tag would
# otherwise produce boxes with no runtime and a red lane ten minutes later
# blaming A2A.
#
# WHY THIS POLLS RATHER THAN CHECKS ONCE. This lane is release-blocking, so it
# runs on a `v*` tag push — at which point the newest reachable v-tag IS the tag
# being released, and the workflow that publishes its agent-runtime bundle
# (release.yml) is running IN PARALLEL with this one. A single check would lose
# that race and report a missing bundle when the truth is "not yet". So wait,
# bounded, and say which of the two it was.
BUNDLE_URL="https://github.com/FootprintAI/Containarium/releases/download/$RELEASE/agent-runtime-bundle.tar.gz"
BUNDLE_WAIT_S="${CONTAINARIUM_E2E_CREW_BUNDLE_WAIT:-1200}"
log "waiting for the agent-runtime bundle for $RELEASE to be published (up to ${BUNDLE_WAIT_S}s; on a tag push release.yml is publishing it right now)"
bundle_code=""
bundle_deadline=$(( $(date +%s) + BUNDLE_WAIT_S ))
while :; do
  bundle_code="$(curl -sSL -o /dev/null -w '%{http_code}' --max-time 60 -I "$BUNDLE_URL" || true)"
  [ "$bundle_code" = "200" ] && break
  [ "$(date +%s)" -ge "$bundle_deadline" ] && break
  sleep 15
done
[ "$bundle_code" = "200" ] \
  || fail "agent-runtime bundle for $RELEASE is still not downloadable after ${BUNDLE_WAIT_S}s ($BUNDLE_URL -> $bundle_code). Either the tag's release.yml has not published it (or failed), or CONTAINARIUM_E2E_CREW_RELEASE names a tag that has no bundle. Either way the member boxes would come up with no in-box loop, so this stops before spending money."
ok "agent-runtime bundle for $RELEASE is published"


# ========================================================================
# The fixture's REAL file list, computed independently of the run.
#
# Load-bearing: assertion 4 asks whether the artifact's paths exist in the
# fixture, and the only way that question means anything is if the answer
# comes from somewhere the run never touched. So it comes from this script's
# own clone of the pinned commit on the HOST, not from the box's checkout and
# not from the artifact.
# ========================================================================
log "cloning the fixture at $FIXTURE_GIT_SHA to compute its real file list"
git init -q "$FIXTURE_CLONE"
git -C "$FIXTURE_CLONE" remote add origin "$FIXTURE_GIT_SOURCE"
git -C "$FIXTURE_CLONE" fetch -q --depth 1 origin "$FIXTURE_GIT_SHA"
git -C "$FIXTURE_CLONE" checkout -q FETCH_HEAD
FIXTURE_PATHS_FILE="$WORKDIR/fixture-paths.txt"
git -C "$FIXTURE_CLONE" ls-tree -r --name-only HEAD >"$FIXTURE_PATHS_FILE"
fixture_path_count="$(wc -l <"$FIXTURE_PATHS_FILE" | tr -d ' ')"
[ "$fixture_path_count" -gt 0 ] || fail "the fixture clone at $FIXTURE_GIT_SHA lists no files — the pin is wrong or the fetch failed"
ok "fixture at $FIXTURE_GIT_SHA holds $fixture_path_count file(s): $(tr '\n' ' ' <"$FIXTURE_PATHS_FILE")"

# The fixture task. Names an EXISTING file explicitly and caps max_files at 1,
# because AC2's claim is about the artifact touching a real path — a task that
# invites a new file would be testing the model's restraint instead of the
# crew's contract. The shape is issue-implementer's documented task contract
# (pkg/core/skills/skills.yaml): {"task": {...}, "constraints": {...}}.
TASK_JSON="$(jq -nc --arg url "$FIXTURE_GIT_SOURCE" '{
  task: {
    title: "README.md does not say what this repository is",
    body: "README.md opens with a greeting and a link but never says what the repository is for. Add one short sentence to the EXISTING README.md explaining that this repository is GitHub'"'"'s fork-and-pull-request tutorial fixture. Edit README.md only; do not create any new file and do not touch index.html or styles.css.",
    url: ($url + "/blob/main/README.md"),
    labels: ["documentation", "good-first-issue"]
  },
  constraints: { max_files: 1 }
}')"

# ========================================================================
# Postgres: real. The crew-run store is Postgres-backed and the run's archive
# row is what GetCrewRun reads back.
# ========================================================================
if [ -z "${CONTAINARIUM_POSTGRES_URL:-}" ]; then
  # Pick a runtime that ANSWERS, not merely one that is on PATH — a host can
  # carry a docker shim whose real binary is gone, and `command -v` cannot tell
  # the difference (the agent-skill-lease-e2e.sh precedent).
  for rt in docker podman; do
    rt_path="$(command -v "$rt" || true)"
    [ -n "$rt_path" ] || continue
    if "$rt_path" info >/dev/null 2>&1; then
      CONTAINER_RUNTIME="$rt_path"
      break
    fi
    log "$rt is on PATH but not usable; trying the next runtime"
  done
  [ -n "$CONTAINER_RUNTIME" ] || fail "set CONTAINARIUM_POSTGRES_URL or install a working docker/podman for a throwaway postgres"
  PG_PORT="$(( (RANDOM % 1000) + 17432 ))"
  PG_CONTAINER="crew-e2e-pg-$$"
  log "starting throwaway postgres on :$PG_PORT"
  "$CONTAINER_RUNTIME" run -d --name "$PG_CONTAINER" \
    -e POSTGRES_USER=containarium -e POSTGRES_PASSWORD=e2e -e POSTGRES_DB=containarium \
    -p "127.0.0.1:${PG_PORT}:5432" postgres:16-alpine >/dev/null
  export CONTAINARIUM_POSTGRES_URL="postgres://containarium:e2e@127.0.0.1:${PG_PORT}/containarium?sslmode=disable"
  # TCP (-h 127.0.0.1) readiness probe: the postgres entrypoint runs a
  # temporary Unix-socket-only server during init and then shuts it down, so a
  # default probe answers "ready" about the wrong server (#1514).
  for _ in $(seq 1 90); do
    "$CONTAINER_RUNTIME" exec "$PG_CONTAINER" pg_isready -h 127.0.0.1 -U containarium >/dev/null 2>&1 && break
    sleep 1
  done
  "$CONTAINER_RUNTIME" exec "$PG_CONTAINER" pg_isready -h 127.0.0.1 -U containarium >/dev/null 2>&1 \
    || fail "postgres did not become ready"
fi

# ========================================================================
# Build the system under test, stamped with the published release tag so the
# member boxes' post_start pulls a REAL agent-runtime (see RELEASE above).
# ========================================================================
log "building containariumd stamped as $RELEASE"
go build -ldflags "-X github.com/footprintai/containarium/pkg/version.Version=$RELEASE" \
  -o "$BIN" ./cmd/containariumd

JWT_SECRET="${CONTAINARIUM_JWT_SECRET:-$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')}"

log "starting daemon (grpc :$GRPC_PORT http :$HTTP_PORT, model gateway provider $PROVIDER from \$$PROVIDER_KEY_ENV, quota ${GATEWAY_QUOTA_CALLS} calls / ${GATEWAY_QUOTA_TOKENS} tokens per ${GATEWAY_QUOTA_WINDOW})"
# SC2024 (sudo doesn't affect redirects) is the intent, not a bug: the log file
# is created by THIS shell inside our own mktemp workdir, so cleanup() can read
# it without sudo. Same shape as scripts/cluster-e2e.sh.
# shellcheck disable=SC2024
sudo env \
  CONTAINARIUM_JWT_SECRET="$JWT_SECRET" \
  CONTAINARIUM_POSTGRES_URL="$CONTAINARIUM_POSTGRES_URL" \
  "$PROVIDER_KEY_ENV=$PROVIDER_KEY" \
  CONTAINARIUM_GATEWAY_QUOTA_WINDOW="$GATEWAY_QUOTA_WINDOW" \
  CONTAINARIUM_GATEWAY_QUOTA_CALLS="$GATEWAY_QUOTA_CALLS" \
  CONTAINARIUM_GATEWAY_QUOTA_TOKENS="$GATEWAY_QUOTA_TOKENS" \
  "$BIN" daemon --port "$GRPC_PORT" --http-port "$HTTP_PORT" \
  >"$DAEMON_LOG" 2>&1 &
DAEMON_PID=$!

DAEMON_WAIT_TRIES="${CONTAINARIUM_E2E_CREW_DAEMON_WAIT_TRIES:-300}"
log "waiting for the daemon's HTTP gateway (up to $((DAEMON_WAIT_TRIES * 2))s)"
for i in $(seq 1 "$DAEMON_WAIT_TRIES"); do
  sudo kill -0 "$DAEMON_PID" 2>/dev/null || fail "daemon exited during startup"
  if daemon_is_ready "$HTTP_PORT" /v1/crews; then
    break
  fi
  [ "$i" = "$DAEMON_WAIT_TRIES" ] && fail "daemon HTTP gateway not answering after $((DAEMON_WAIT_TRIES * 2))s"
  sleep 2
done

# The gateway must be mounted, or the boxes run in direct mode with no
# credential at all, every model call fails "Not logged in", and the lane would
# go red for a reason that has nothing to do with engineer-crew.
gw_health="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$HTTP_PORT/__gateway/healthz")"
[ "$gw_health" = "200" ] \
  || fail "model gateway is not mounted (/__gateway/healthz -> $gw_health); \$$PROVIDER_KEY_ENV was not picked up, so no member box would have a model credential"
ok "model gateway mounted for provider $PROVIDER (/__gateway/healthz -> 200)"

# admin role: provisionSkillBox calls AuthorizeTenant on each member box's
# tenant name ("agent-<skill>"), which only the tenant itself or an admin
# passes. Wildcard scopes: a member's in-box token is the intersection of the
# caller's scopes and the manifest's, and this caller must not be the thing
# that narrows it.
TOKEN="$("$BIN" token generate --username crew-e2e --roles admin --scopes '*' \
  --expiry 2h --secret "$JWT_SECRET" --raw)"
[ -n "$TOKEN" ] || fail "could not mint a caller token"

read_out() { cat "$1" 2>/dev/null || true; }
read_code() {
  local c
  c="$(read_out "$1")"
  [ -n "$c" ] || c="000"
  printf '%s' "$c"
}

# ========================================================================
# Pre-provision the member boxes and verify the in-box loop EXISTS, before
# committing any spend.
#
# Deploying the agent-runtime recipe under exactly the box name
# provisionSkillBox derives ("agent-" + skill id) means the crew run below
# takes that function's idempotent REUSE path: it skips deploy and just
# re-mints, re-seeds and re-applies policy. So this is not a special path
# through the daemon — it is the same path every second-and-later run of a
# skill takes, and it costs no model tokens.
# ========================================================================
for m in $CREW_MEMBERS; do
  log "pre-provisioning member box agent-$m (recipe agent-runtime, release $RELEASE) — the recipe's post_start installs Node, this is the slow part"
  : >"$WORKDIR/deploy-$m.body"
  deploy_code="$(curl -sS -o "$WORKDIR/deploy-$m.body" -w '%{http_code}' \
    -X POST "http://127.0.0.1:$HTTP_PORT/v1/recipes/agent-runtime/deploy" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg name "agent-$m" --arg rel "$RELEASE" '{name: $name, parameters: {release: $rel}}')" || true)"
  if [ "$deploy_code" != "200" ]; then
    echo "---- deploy response ----"; read_out "$WORKDIR/deploy-$m.body"; echo
    fail "deploying agent-$m returned $deploy_code (want 200); the member box does not exist, so the crew run would fail on provisioning"
  fi
  sudo incus exec "agent-$m-container" -- test -x /usr/local/bin/agent-runtime \
    || fail "agent-$m-container has no /usr/local/bin/agent-runtime after deploying release $RELEASE — install-agent-runtime.sh's assembly is best-effort and evidently skipped or failed. Without the in-box loop the crew's A2A hop cannot be served, and the failure would otherwise appear only AFTER the model spend. Check the box's post_start output: sudo incus exec agent-$m-container -- journalctl -b | tail"
  sudo incus exec "agent-$m-container" -- test -x /usr/local/bin/agent-box \
    || fail "agent-$m-container has no /usr/local/bin/agent-box — the agent's MCP tool surface is missing, so it could not edit a file even with a working model"
  ok "member box agent-$m-container has agent-runtime and agent-box on PATH"

  # Cost ceilings the daemon does not set for us. The daemon's serve-mode exec
  # is `bash -lc`, a LOGIN shell, so /etc/profile.d is sourced before
  # agent-runtime starts; CONTAINARIUM_AGENT_ENGINE / _MODE / AGENT_SEED_DIR
  # are set as a command prefix by the daemon and correctly win over anything
  # here, while _MODEL and _MAX_TURNS are not, which is exactly the seam this
  # uses. Same spirit as agent-skill-lease-e2e.sh pushing a stub runtime: the
  # harness configures the box it is measuring, through a documented seam.
  profile_src="$WORKDIR/99-e2e-model-$m.sh"
  {
    echo "# installed by scripts/engineer-crew-e2e.sh — cost ceilings for this lane"
    echo "export CONTAINARIUM_AGENT_MAX_TURNS=$AGENT_MAX_TURNS"
    if [ -n "$AGENT_MODEL" ]; then echo "export CONTAINARIUM_AGENT_MODEL=$AGENT_MODEL"; fi
  } >"$profile_src"
  sudo incus file push --mode 0644 "$profile_src" "agent-$m-container/etc/profile.d/99-e2e-model.sh"
  sudo incus exec "agent-$m-container" -- test -f /etc/profile.d/99-e2e-model.sh \
    || fail "could not install the cost-ceiling profile into agent-$m-container"
  ok "cost ceilings installed in agent-$m-container (max_turns=$AGENT_MAX_TURNS model=${AGENT_MODEL:-<engine default>})"
done

# ========================================================================
# THE MEASURED RUN — the only model spend in this script.
# ========================================================================
RUN_ID="crew-e2e-$(head -c4 /dev/urandom | od -An -tx1 | tr -d ' \n')"
RUN_SEED_DIR="$AGENT_SEED_ROOT/$RUN_ID"
RUN_WORKSPACE_DIR="$AGENT_WORKSPACE_ROOT/$RUN_ID"

run_crew() {
  local out="$1" body
  body="$(jq -nc --arg crew "$CREW_ID" --arg run_id "$RUN_ID" --arg input "$TASK_JSON" \
    --arg src "$FIXTURE_GIT_SOURCE" --arg ref "$FIXTURE_GIT_SHA" \
    '{crew_id: $crew, run_id: $run_id, input_json: $input, git_source: $src, git_ref: $ref}')"
  : >"$out.body"
  curl -sS -o "$out.body" -w '%{http_code}' --max-time "$RUN_TIMEOUT_S" \
    -X POST "http://127.0.0.1:$HTTP_PORT/v1/crews/$CREW_ID/run" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d "$body" >"$out.code" 2>"$out.err" || true
