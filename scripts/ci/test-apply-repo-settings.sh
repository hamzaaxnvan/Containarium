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
