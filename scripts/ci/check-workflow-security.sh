#!/usr/bin/env bash
# Fail on the workflow security rules the supply-chain gate enforces (#2904,
# follow-up to #2793 / #2649), using zizmor.
#
# Usage:
#   scripts/ci/check-workflow-security.sh [path ...]   # default: .github/workflows .github/actions
#   scripts/ci/check-workflow-security.sh --self-test
#
# Enforced rules (zizmor audit ids), at severity and confidence >= medium:
#   dangerous-triggers     pull_request_target / workflow_run (the way a PR head
#                          gets checked out with a write token and secrets)
#   template-injection     ${{ }} event data expanded inside a run:/script block
#   excessive-permissions  missing permissions:, write-all, or a write scope
#                          granted at workflow level where a job can hold it
# The auditor persona is used so these rules report everything they can; every
# other zizmor audit is reported by zizmor but does not fail the lane (the
# filter below). Adding a rule to the gate = adding its id to RULES and a
# bad-*.yml fixture under scripts/ci/testdata/zizmor/.
#
# Per-finding suppression goes in .github/zizmor.yml (`rules.<id>.ignore`),
# each with a reason comment; there is no blanket disable.
#
# zizmor runs offline: the lane has no extra token and the three rules need no
# network. Set ZIZMOR=/path/to/zizmor to choose the binary.
set -euo pipefail

ZIZMOR="${ZIZMOR:-zizmor}"
RULES="dangerous-triggers template-injection excessive-permissions"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG="$HERE/../../.github/zizmor.yml"

# Reads zizmor's JSON on stdin; prints each enforced finding and exits 1 if any.
FILTER=$(cat <<'PY'
import json, os, sys
rules = set(os.environ["RULES"].split())
rank = {"Unknown": 0, "Low": 1, "Medium": 2, "High": 3}
bad = 0
for f in json.load(sys.stdin):
    d = f["determinations"]
    if f.get("ignored") or f["ident"] not in rules:
        continue
