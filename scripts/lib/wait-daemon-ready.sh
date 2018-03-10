#!/usr/bin/env bash
# wait-daemon-ready.sh — shared readiness probe for the e2e lanes that start a
# real daemon (#3143 follow-up).
#
# Since the daemon binds its HTTP port early, a daemon that is still waiting for
# core containers answers EVERY path except /health with
# `503 daemon is starting: ...`. A bare `curl -s -o /dev/null URL` succeeds on
# any response, so it proves only that the port is bound, not that the daemon
# is ready. A ready daemon answers the probe paths with anything except 503
# (200, or 401/403 for an unauthenticated probe); 000 means no connection.
#
# Source this file; it defines functions only.

