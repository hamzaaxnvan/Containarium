#!/bin/sh
# containarium-otel-sidecar entrypoint
#
# Validates the identity env vars Containarium stamps via the LXC's
# --monitoring flag before handing off to otelcol-contrib. Fail-closed
# per the platform-sidecar contract: missing required env = exit
# non-zero with a clear message, so `docker compose up` reports it.
#
# See docs/OTEL-AGENT-RELAY-DESIGN.md.

