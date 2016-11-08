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

  files="$(git diff --no-renames --name-only "$base" "$head")"
  if [ -z "$files" ]; then
    echo "empty diff" >&2
    echo false
    return
  fi

  while IFS= read -r f; do
    case "$f" in
      CHANGELOG.md | pkg/version/version.go) ;;
      *)
        echo "touches $f" >&2
        echo false
        return
        ;;
    esac
  done <<<"$files"

  # Every changed line in version.go must be the Version constant.
  other="$(git diff --no-renames -U0 "$base" "$head" -- pkg/version/version.go \
    | grep -E '^[+-]' \
    | grep -vE '^(\+\+\+|---) ' \
    | grep -vE '^[+-][[:space:]]*Version[[:space:]]*=[[:space:]]*"[^"]*"[[:space:]]*$' || true)"
  if [ -n "$other" ]; then
    echo "pkg/version/version.go changes more than the Version constant" >&2
    echo false
    return
  fi

  echo "release-only diff: $(tr '\n' ' ' <<<"$files")" >&2
  echo true
}

self_test() {
  local dir fails=0 base
  dir="$(mktemp -d)"
  trap 'rm -rf "$dir"' RETURN
  git -C "$dir" init -q
  git -C "$dir" config user.email t@example.com
  git -C "$dir" config user.name t
  mkdir -p "$dir/pkg/version" "$dir/internal"
  printf '# Changelog\n\n## [Unreleased]\n' >"$dir/CHANGELOG.md"
