#!/usr/bin/env bash
# Fake `gh` for scripts/ci/test-apply-repo-settings.sh. Serves canned JSON from
# $FAKE_DIR/before until a write arrives, then from $FAKE_DIR/after, and logs
# every call to $FAKE_LOG as "METHOD path" (bodies to $FAKE_LOG.body).
set -euo pipefail
[[ "$1" == api ]] || exit 2
shift
method=GET
if [[ "$1" == -X ]]; then method=$2; shift 2; fi
path=$1
if [[ "$method" != GET ]]; then
  echo "$method $path" >>"$FAKE_LOG"
  if [[ "${2:-}" == --input ]]; then
    { echo "== $method $path"; cat; echo; } >>"$FAKE_LOG.body"
  fi
