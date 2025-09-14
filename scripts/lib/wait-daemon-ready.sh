#!/usr/bin/env bash
# wait-daemon-ready.sh — shared readiness probe for the e2e lanes that start a
# real daemon (#3143 follow-up).
#
# Since the daemon binds its HTTP port early, a daemon that is still waiting for
# core containers answers EVERY path except /health with
