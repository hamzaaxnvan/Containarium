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
