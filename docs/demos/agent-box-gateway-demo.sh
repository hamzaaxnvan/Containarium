#!/usr/bin/env bash
# =============================================================================
# Containarium — DEMO: an AI agent in a throwaway, isolated box,
#                       talking to models through YOUR own gateway.
#
#   Self-hosted · Apache-2.0 · your compute · your key · your audit log
#   Target length when recorded: ~90 seconds.
#
# THE WEDGE (what this demo proves in one minute):
#   1. A skill = a packaged agent (a box recipe + a typed manifest).
#   2. `agent run` spins a disposable, network-isolated box, mints a token
#      scoped to EXACTLY that skill's permissions, and runs the agent.
#   3. The agent's model calls are brokered by the daemon's model-gateway:
#      the provider API key lives on the HOST and NEVER enters the box —
#      the box only ever holds a short-lived, revocable token. Every call
#      is metered per tenant/skill/model.
#   4. It's a box: tear it down, zero blast radius.
#
# -----------------------------------------------------------------------------
# PREREQUISITES (operator, one-time — do this BEFORE recording):
#
#   * A reachable Containarium daemon ($SERVER below). Self-host:
#       curl -fsSL https://containarium.dev/install.sh | sh   # (or build OSS)
#
#   * Turn on the gateway by giving the DAEMON one provider key in its env,
#     then (re)start it. The key turns the model-gateway on automatically:
#       # /etc/containarium/daemon.env
#       GEMINI_API_KEY=...            # or ANTHROPIC_API_KEY / OPENAI_API_KEY
#     The daemon logs on boot:
#       "Model-gateway enabled (providers=[gemini ...]) — provider keys never leave the host"
#
#   The `code-review` skill below ships in OSS, so no extra pack is needed.
#   (Opinionated/domain skills — PM, compliance — load from a pack via
#    CONTAINARIUM_SKILLS_DIR; `hello-agent` is the zero-config smoke test.)
#
SERVER="${SERVER:-https://YOUR-DAEMON:8080}"
SKILL="${SKILL:-code-review}"
INPUT="${INPUT:-{\"brief\":\"Review this Go handler for bugs and security issues: func GetUser(w http.ResponseWriter, r *http.Request) { id := r.URL.Query().Get(\\\"id\\\"); row := db.QueryRow(\\\"SELECT name FROM users WHERE id = \\\" + id); ... }\"}}"
