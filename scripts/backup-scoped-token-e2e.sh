#!/usr/bin/env bash
# Real daemon + Incus + PostgreSQL scope contract for #2401.
# Run as root against a disposable daemon with JWT_SECRET and ADMIN_TOKEN.
set -euo pipefail

bin=${CONTAINARIUM_BIN:-/tmp/containarium}
server=${CONTAINARIUM_BACKUP_E2E_SERVER:-http://localhost:8080}
image=${CONTAINARIUM_LANE_IMAGE:-images:ubuntu/24.04}
: "${JWT_SECRET:?required}"
: "${ADMIN_TOKEN:?required}"
tenant="verifylane$$"
source_name="$tenant-container"
scratch_dir=$(mktemp -d)
cleanup() {
  incus delete "$source_name" --force >/dev/null 2>&1 || true
  rm -rf "$scratch_dir"
}
trap cleanup EXIT

# api <expected-status> <out-file> <curl args...> <url>
# Runs one request, keeps the response body in <out-file>, and fails unless
# the HTTP status equals <expected-status>. On a mismatch it prints the
# URL, the status, and the response body, so a failure in CI shows
# its cause (#3028). Authorization headers are never echoed.
api() {
  local want=$1 out=$2 code url
  shift 2
  url=${*: -1}
  if ! code=$(curl -sS -o "$out" -w '%{http_code}' "$@"); then
    echo "request failed before a response: $url" >&2
    return 1
  fi
  if [ "$code" != "$want" ]; then
    echo "Expected HTTP $want from $url, received $code; response body:" >&2
    cat "$out" >&2 || true
    echo >&2
    return 1
  fi
}
printf '%s' "$JWT_SECRET" > "$scratch_dir/jwt.secret"
chmod 600 "$scratch_dir/jwt.secret"
