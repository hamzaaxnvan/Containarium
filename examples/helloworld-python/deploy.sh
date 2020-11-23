#!/usr/bin/env bash
# Runs inside the container as the post-receive deploy hook (configured
# via mcp__push --deploy_cmd "bash deploy.sh"). Four jobs:
#   1. Install Python deps (containarium-telemetry for OTel wiring).
#   2. Snapshot the current commit SHA so app.py can read it at request time.
