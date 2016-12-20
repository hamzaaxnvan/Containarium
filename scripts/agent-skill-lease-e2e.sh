#!/usr/bin/env bash
#
# agent-skill-lease-e2e.sh — the executable proof of the run-lease property
# (#1819): a skill run's credentials die with the run.
#
# Design: docs/architecture/execution-scoped-authorization.md (cloud repo),
# "End-to-end for the daemon" under Test strategy. That design's headline
# metric is a duration — "the run's gateway token is 401 within 5 s of the
# run returning" — and a duration cannot be asserted by a unit test with a
# fake clock and a fake revocation store. This script is what makes the
# metric MEASURED rather than reasoned about: real daemon, real Postgres,
# real Incus box, real model-gateway, real wall clock.
#
# What it asserts, in order (each prints OK with what it observed; the first
# miss exits non-zero):
#
#   1. during the run   — the run's gateway token, read out of the box's
#                         /etc/containarium/agent/gateway.env WHILE the run is
#                         still executing, is ACCEPTED by /v1/model/<provider>:
#                         the answer is anything except the gateway's own
#                         credential rejections.
#   2. after the run    — the same token, same call, is exactly 401
#                         "gateway token revoked", observed within
#                         CONTAINARIUM_E2E_LEASE_REVOKE_BUDGET_MS of the RPC
#                         returning.
#   3. seed files gone  — `ls /etc/containarium/agent/` in the box shows
#                         neither `token` nor `gateway.env`.
#   4. audit trail      — `containarium audit query --run-id <id>` returns both
#                         the agent.run_lease_issue and agent.run_lease_end
#                         rows.
#
# HOW ASSERTION 1 IS POSSIBLE AT ALL. RunAgentSkill is synchronous: it returns
# only after the in-box agent exec finishes, and the deferred endRunLease has
# already revoked and wiped by the time the HTTP response is written. So there
# is no window to observe "accepted" after the call. The script therefore issues
# the RPC in the BACKGROUND and reads gateway.env out of the box while the call
# is in flight — which needs the run to last long enough to be observed. That is
# what the stub agent-runtime is for: the box's /usr/local/bin/agent-runtime is
# replaced with `sleep <n>`, so the run's duration is a knob this script owns
# rather than a race it hopes to win. The design's phrase "read from gateway.env
# during the run" is exactly this.
#
# WHAT THE 5 s BUDGET IS AND IS NOT MEASURING — read this before tuning it.
# `endRunLease` is a `defer` INSIDE RunAgentSkill (internal/server/agent_server.go),
# so both revokes and the agent.run_lease_end audit write have already completed
# by the time the HTTP response is written. EXIT_MS is therefore taken when the
# revocation is ALREADY durable in Postgres, and the ~10-20 ms this lane observes
# is one local HTTP round-trip — it is NOT the design's 2 s + 2 s + 3 s worst
# case, which is absorbed inside the run's own duration and never appears in this
# window at all.
#
# So assertion 2 is, structurally, a did-it-happen-at-all check wearing a
# duration's clothes, and that is fine: the PRD states a bound, this measures the
# bound, and the sabotage below proves the assertion discriminates. Two
# consequences follow, and both are deliberate:
#   - enforcing the bound STRICTLY is safe, because the expected value is three
#     orders of magnitude under it. A measurement anywhere near 5 s means
#     something structural changed (revocation moved off the synchronous path,
#     Postgres is wedged, the gateway grew a cache) and is worth a red lane.
#   - the ONLY way a healthy run can produce a near-budget number is a poll
#     request that straddles the deadline, which is why POLL_MAX_TIME is small
#     and why the budget is checked against the MEASUREMENT rather than only
#     used as the poll loop's continuation condition.
#
# WHY A WARM-UP RUN FIRST. The stub can only be installed into a box that
# exists, and the box is created by the first run of the skill. So the script
# runs the skill once (its own run id, its own lease, fully revoked and wiped on
# exit like any other run), installs the stub into the resulting box, and then
# does the MEASURED run — which takes provisionSkillBox's idempotent reuse path,
# re-mints, re-seeds, and execs the stub. Nothing about the credential lifecycle
# differs between the two; only the run's duration does.
#
# WHY A DUMMY PROVIDER KEY IS ENOUGH. The gateway's credential checks —
# signature, provider binding, and the revocation kill-switch — all run BEFORE
# the provider key is looked up (internal/modelgateway/gateway.go: "Deliberately
# ahead of the key lookup and the Director, so a revoked token never causes the
# real provider key to be touched"). The assertions here only ever distinguish
# "the gateway rejected this credential" from "the gateway accepted it and went
# upstream", so whatever the upstream says — a provider auth error, a 502
# because the runner has no egress — is equally good evidence of acceptance. No
# real key, no real model response, no provider spend.
#
# Local use:
#   sudo -v && bash scripts/agent-skill-lease-e2e.sh
#
# Prove-it-can-fail (the #1819 acceptance criterion, and the #1418 guardrail
# this repo applies to every e2e lane). TWO sabotages, because assertion 2 makes
# two separate claims and a lane should be able to fail each of them on demand:
#
#   CONTAINARIUM_E2E_SABOTAGE=no-revoke bash scripts/agent-skill-lease-e2e.sh
# "the credential is dead". Deletes the run_exit rows the daemon just wrote to
# jwt_revocations, which leaves the system in exactly the state a missing revoke
# call produces — the gateway's lookup finds no row and answers the way it did
# before #1817. Assertions 1, 3 and 4 stay green, so the red is attributable to
# the revoke and nothing else.
#
#   CONTAINARIUM_E2E_SABOTAGE=slow-revoke bash scripts/agent-skill-lease-e2e.sh
# "and it is dead WITHIN THE BUDGET". Delays the first poll past the budget, so
# the token really is revoked and the observation really is late. This exists
# because the budget was once only the poll loop's continuation condition and
# never a gate on the measurement, which let a straddling request report
# "token DEAD 6009ms … (budget 5000ms)" and exit 0. Now it exits non-zero, and
# this sabotage is how that stays true.
#
# Both are expected to exit NON-ZERO on assertion 2; the workflow's
# prove-lease-lane-can-fail job asserts it.
#
# Host requirements:
#   - Incus with a usable storage pool at /var/lib/incus/unix.socket. No KVM:
#     a skill box is an Incus system container, which is why this lane runs on
#     a stock GitHub-hosted runner (see scripts/ci-incus-container-host.sh).
#   - Go toolchain, passwordless sudo, curl, jq
#   - Postgres via CONTAINARIUM_POSTGRES_URL, or docker/podman so this script
#     can start a throwaway one. Postgres is NOT optional here: the revocation
#     store and the audit rows are both Postgres-backed, and they are the
#     property under test.
#   - Egress for the skill box's first provision (the agent-runtime recipe's
#     post_start installs Node). Egress to the model provider is NOT required.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
# shellcheck source=/dev/null
. "$REPO_ROOT/scripts/lib/wait-daemon-ready.sh"

# Ports deliberately off cluster-e2e.sh's (15051/18080) so the two lanes can
# share a runner without fighting over a listener.
GRPC_PORT="${CONTAINARIUM_E2E_LEASE_GRPC_PORT:-15071}"
HTTP_PORT="${CONTAINARIUM_E2E_LEASE_HTTP_PORT:-18090}"

# The skill under test. hello-agent is the catalog's neutral reference skill
# (pkg/core/skills/skills.yaml) — deliberately reused rather than inventing a
# fixture, since the credential lifecycle is identical for every skill.
SKILL_ID="${CONTAINARIUM_E2E_LEASE_SKILL:-hello-agent}"

# How long the stub agent-runtime sleeps, i.e. how long the measured run lasts.
# The design says `sleep 2`; the default here is longer because assertion 1 has
# to fit inside this window on a cold runner — an exec into the box plus a
# proxied HTTP call that may sit on a connect timeout to a provider the runner
# cannot reach. 2s is a coin flip; 20s is not, and a longer run cannot make a
# revocation look faster than it is.
RUN_SECONDS="${CONTAINARIUM_E2E_LEASE_RUN_SECONDS:-20}"

# The PRD's headline metric, in milliseconds. Assertion 2 fails if the token was
# not OBSERVED revoked within this long past the RPC returning — see the header's
# "WHAT THE 5 s BUDGET IS AND IS NOT MEASURING".
REVOKE_BUDGET_MS="${CONTAINARIUM_E2E_LEASE_REVOKE_BUDGET_MS:-5000}"

# Assertion 2 keeps polling for this much longer than the budget before giving
# up. The extra time never buys a PASS — the gate is the budget — it buys a
# better FAILURE: "dead, but only after 6009ms" names what went wrong, where
# "never seen dead within 5000ms" leaves the reader unable to tell a late
# revocation from an absent one.
POLL_GRACE_MS="${CONTAINARIUM_E2E_LEASE_POLL_GRACE_MS:-10000}"

# Per-request ceilings, in seconds.
#
# MODEL_CALL_MAX_TIME is for assertion 1, the ONE call in this script that
# actually reaches the provider (every post-revocation poll short-circuits inside
# the gateway), so it has to tolerate a runner that cannot reach the provider at
# all and must wait out a connect timeout.
#
# POLL_MAX_TIME is deliberately much smaller, and that is a correctness
# property, not a tuning preference: assertion 2's measurement is only as tight
# as the request that produced it, so a poll started just inside the budget with
# a 15 s ceiling could only ever return an over-budget observation. Small enough
# that a straddle is bounded; the gate after the loop catches one anyway.
MODEL_CALL_MAX_TIME="${CONTAINARIUM_E2E_LEASE_MODEL_MAX_TIME:-15}"
POLL_MAX_TIME="${CONTAINARIUM_E2E_LEASE_POLL_MAX_TIME:-3}"

SABOTAGE="${CONTAINARIUM_E2E_SABOTAGE:-}"

# The gateway provider the daemon provisions skill boxes for. Set by the dummy
# key below; kept a variable because the /v1/model/<provider> path and the
# gateway.env variable names are both per-provider.
PROVIDER="anthropic"
GW_TOKEN_VAR="ANTHROPIC_AUTH_TOKEN"

# #1861: the coding-skill-on-a-repo proof. octocat/Spoon-Knife is GitHub's own
# decade-old fork-tutorial fixture — small, public, and untouched since 2014,
# so pinning its tip commit is as close to "will never change" as a third
# party's repo gets. Its README.md's first line is non-blank ("### Well hello
# there!"), which is what makes assertion 6 possible without a model: the
# stub cites whatever grep -n actually finds, this fixture just guarantees
# that citation is never empty.
FIXTURE_GIT_SOURCE="${CONTAINARIUM_E2E_LEASE_FIXTURE_REPO:-https://github.com/octocat/Spoon-Knife}"
FIXTURE_GIT_SHA="${CONTAINARIUM_E2E_LEASE_FIXTURE_SHA:-d0dd1f61b33d64e29d8bc1372a94ef6a2fee76a9}"
# Obviously fake, and never a real secret. NOT ignored by GitHub, despite
# the design doc's "Deviations" note assuming a public repo makes a bad
# Authorization header harmless — verified otherwise (see assertion 8): any
# credential presented is validated and an invalid one is rejected outright.
# So this is used on a run EXPECTED to fail its own fetch — its only job is
# to be a string that never appears in that failure's response or the
# daemon log.
FIXTURE_GIT_CREDENTIAL="e2e-fake-credential-1861-$$-not-a-real-secret"
GIT_STUB_MARKER="containarium-lease-e2e-git-stub"

# #1860: every run gets its own seed directory (and, when it fetches a repo
# per #1859, its own workspace) under these fixed roots — matching the
# daemon's agentSeedRoot/agentWorkspaceRoot (internal/server/agent_server.go).
# There is no longer a single box-level SEED_DIR; a run's actual directory is
# "$AGENT_SEED_ROOT/$RUN_ID", computed below once RUN_ID is known.
AGENT_SEED_ROOT="/etc/containarium/agent/runs"
AGENT_WORKSPACE_ROOT="/workspace/runs"
BOX="agent-${SKILL_ID}-container"

WORKDIR="$(mktemp -d)"
BIN="$WORKDIR/containariumd"
DAEMON_LOG="$WORKDIR/daemon.log"
PG_CONTAINER=""
CONTAINER_RUNTIME=""
DAEMON_PID=""

log() { echo "==> $*"; }
ok() { echo "OK   $*"; }
fail() { echo "FATAL: $*" >&2; exit 1; }

# now_ms is the clock every duration in this script is measured on. GNU date;
# the lane runs on Linux only.
now_ms() { date +%s%3N; }

# --- pre-flight: fail loudly, never skip silently -----------------------
[ -S /var/lib/incus/unix.socket ] || fail "no Incus socket at /var/lib/incus/unix.socket"
command -v go >/dev/null || fail "no Go toolchain"
command -v curl >/dev/null || fail "no curl"
command -v jq >/dev/null || fail "no jq (the RPC response is JSON and this script asserts on its fields)"
sudo -n true 2>/dev/null || fail "needs passwordless sudo (daemon and Incus operations run as root)"
case "$SABOTAGE" in
  ''|no-revoke|slow-revoke|skip-workspace-removal) ;;
  *) fail "unknown CONTAINARIUM_E2E_SABOTAGE=$SABOTAGE (want empty, 'no-revoke', 'slow-revoke' or 'skip-workspace-removal')" ;;
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
  # The skill box is named deterministically from the skill id, so a red run
  # would otherwise hand its box to the next run — which then takes the REUSE
  # path with a stale stub and never exercises a first provision.
  if sudo incus info "$BOX" >/dev/null 2>&1; then
    log "sweeping skill box $BOX"
    sudo incus delete --force "$BOX" 2>/dev/null
  fi
  if [ -n "$PG_CONTAINER" ]; then
    log "stopping throwaway postgres"
    "$CONTAINER_RUNTIME" rm -f "$PG_CONTAINER" >/dev/null 2>&1
  fi
  if [ $status -ne 0 ] && [ -f "$DAEMON_LOG" ]; then
    # The lease lines first, over the WHOLE log: whether the gateway was
    # enabled at all, and whether a credential went unrevoked, are both
    # decided in one line the daemon prints once. A tail alone cannot answer
    # "was there a revocation store?", which is the first question every red
    # run in this lane raises.
    echo "---- daemon log: gateway / run-lease lines (whole run) ----"
    grep -E 'model-gateway|agent-skill|Model-gateway|revocation' "$DAEMON_LOG" || echo "(none)"
    echo "---- daemon log (last 120 lines) ----"
    tail -120 "$DAEMON_LOG"
  fi
  # The daemon runs under sudo and leaves root-owned trees here.
  sudo rm -rf "$WORKDIR" 2>/dev/null || rm -rf "$WORKDIR"
  exit $status
}
trap cleanup EXIT

# --- postgres: real, and not optional in this lane ----------------------
# Same shape as scripts/cluster-e2e.sh, including the TCP (-h 127.0.0.1)
# readiness probe: the postgres entrypoint runs a temporary Unix-socket-only
# server during init and then shuts it down, so a default probe answers
# "ready" about the wrong server (#1514).
if [ -z "${CONTAINARIUM_POSTGRES_URL:-}" ]; then
  # Pick a runtime that ANSWERS, not merely one that is on PATH. A host can
  # carry a docker shim whose real binary is gone (a nested-container wrapper
  # left behind, say), and `command -v` cannot tell the difference — the lane
  # would then fail minutes later on `docker run` with a shell error that
  # reads like a bug in this script.
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
  PG_PORT="$(( (RANDOM % 1000) + 16432 ))"
  PG_CONTAINER="lease-e2e-pg-$$"
  log "starting throwaway postgres on :$PG_PORT"
  "$CONTAINER_RUNTIME" run -d --name "$PG_CONTAINER" \
    -e POSTGRES_USER=containarium -e POSTGRES_PASSWORD=e2e -e POSTGRES_DB=containarium \
    -p "127.0.0.1:${PG_PORT}:5432" postgres:16-alpine >/dev/null
  export CONTAINARIUM_POSTGRES_URL="postgres://containarium:e2e@127.0.0.1:${PG_PORT}/containarium?sslmode=disable"
  for _ in $(seq 1 90); do
    "$CONTAINER_RUNTIME" exec "$PG_CONTAINER" pg_isready -h 127.0.0.1 -U containarium >/dev/null 2>&1 && break
    sleep 1
  done
  if ! "$CONTAINER_RUNTIME" exec "$PG_CONTAINER" pg_isready -h 127.0.0.1 -U containarium >/dev/null 2>&1; then
    echo "---- postgres container state ----"
    "$CONTAINER_RUNTIME" ps -a --filter "name=$PG_CONTAINER" 2>&1 || true
    echo "---- postgres container log ----"
    "$CONTAINER_RUNTIME" logs "$PG_CONTAINER" 2>&1 | tail -40 || true
    fail "postgres did not become ready"
  fi
elif [ "$SABOTAGE" = no-revoke ]; then
  fail "CONTAINARIUM_E2E_SABOTAGE=no-revoke needs the throwaway postgres this script starts (it deletes revocation rows through it); unset CONTAINARIUM_POSTGRES_URL"
fi

# psql_in_pg runs SQL against the throwaway postgres. Only the sabotage path
# uses it — the assertions themselves go through the product's own surfaces
# (the gateway, `incus exec`, `containarium audit query`), never SQL.
psql_in_pg() {
  "$CONTAINER_RUNTIME" exec "$PG_CONTAINER" \
    psql -q -At -U containarium -d containarium -c "$1"
}

# --- build the system under test ----------------------------------------
# cmd/containariumd is the full daemon AND the operator CLI (`audit query`
# lives there too, untagged) — one binary serves both halves of this lane.
log "building containariumd"
go build -o "$BIN" ./cmd/containariumd

# --- daemon ---------------------------------------------------------------
JWT_SECRET="${CONTAINARIUM_JWT_SECRET:-$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')}"

