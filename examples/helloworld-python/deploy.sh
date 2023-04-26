#!/usr/bin/env bash
# Runs inside the container as the post-receive deploy hook (configured
# via mcp__push --deploy_cmd "bash deploy.sh"). Four jobs:
#   1. Install Python deps (containarium-telemetry for OTel wiring).
#   2. Snapshot the current commit SHA so app.py can read it at request time.
#   3. Install the systemd unit if it isn't installed yet.
#   4. Restart the service so the new code takes effect.
set -euo pipefail

cd "$HOME/work"

