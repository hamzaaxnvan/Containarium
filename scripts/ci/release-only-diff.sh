#!/usr/bin/env bash
# Prints "true" when the diff BASE..HEAD is a release cut and nothing else,
# "false" otherwise. Reasons go to stderr.
#
#   scripts/ci/release-only-diff.sh <base> <head>
#   scripts/ci/release-only-diff.sh --self-test
#
# A release cut (docs/RELEASE-PROCESS.md) changes exactly two files:
# CHANGELOG.md, and the `Version = "..."` constant in pkg/version/version.go.
# Such a diff cannot change what the test, lint and SAST jobs measure, so
# those jobs may skip on it — but only when BASE itself already passed them,
# which the caller (.github/actions/release-only-diff) checks separately.
#
# Deliberately narrow. Anything else, including the sdlc-skills lock that a
# cut sometimes bumps, any other line in version.go, a rename or an empty
# diff, prints "false" and the caller runs the full suite.
set -euo pipefail

release_only() {
  local base="$1" head="$2" files f other

