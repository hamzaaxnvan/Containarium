#!/usr/bin/env bash
# Prove scripts/lib/wait-daemon-ready.sh waits through the daemon's early-bind
# 503s, and that the old `curl -s -o /dev/null` probe would not (#3143 follow-up).
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
. "$ROOT/scripts/lib/wait-daemon-ready.sh"

TMP="$(mktemp -d)"; trap 'kill "$SRV" 2>/dev/null; rm -rf "$TMP"' EXIT
PORT=$((20000 + RANDOM % 20000))
# Stub daemon: 503 for the first N requests to /v1/x, then 200.
cat > "$TMP/stub.py" <<'PY'
import sys, http.server
