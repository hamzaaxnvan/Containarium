#!/usr/bin/env bash
# Fake `gh` for scripts/ci/test-apply-repo-settings.sh. Serves canned JSON from
# $FAKE_DIR/before until a write arrives, then from $FAKE_DIR/after, and logs
# every call to $FAKE_LOG as "METHOD path" (bodies to $FAKE_LOG.body).
set -euo pipefail
[[ "$1" == api ]] || exit 2
shift
