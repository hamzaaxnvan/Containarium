#!/usr/bin/env bash
# Tests for scripts/admin/apply-repo-settings.sh against a fake gh:
#   - dry run (the default) performs no write call;
#   - --apply keeps the required status-check contexts, app ids, strict and
#     enforce_admins in the protection body it sends;
#   - --apply fails when the read-back does not match (seeded negative);
#   - the allow-list check fails on a seeded unlisted action.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
script=$here/../admin/apply-repo-settings.sh
fix=$here/testdata/repo-settings
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
fail=0
t() { echo "FAIL: $*"; fail=1; }

run() { # run NAME FIXDIR args...
  local name=$1 dir=$2; shift 2
  rm -f "$tmp/$name.log" "$tmp/$name.log.body" "$tmp/$name.log.wrote"
  FAKE_DIR=$dir FAKE_LOG=$tmp/$name.log GH=$here/fake-gh-repo-settings.sh \
    "$script" "$@" >"$tmp/$name.out" 2>&1
}

# 1. dry run: exit 0, no write calls, says so
