#!/usr/bin/env bash
# Fail when a build-time network fetch appears that is not in the inventory.
#
# A "fetch" is a command in a workflow, composite action, Makefile, *.mk,
# shell script (*.sh, *.bash) or Dockerfile that runs one of: curl, wget,
# go install, go get, npm/pnpm/yarn install|ci|add, npx, pip/pipx/uv, cargo
# install, gem install, git clone, docker pull, xcaddy build, or a Dockerfile
# `ADD <url>`. Backslash-continued lines are joined first, so a split command
# is still seen.
#
# The inventory, scripts/ci/build-fetches.allow, has one line per fetch:
#   <path> TAB <tool> TAB <fingerprint> TAB <snippet>
# The fingerprint is the first 12 hex of sha256 over the whitespace-normalised
# command text from the tool name to the end of the (joined) line. It is keyed
# by file, tool and content, never by line number, so moving a line does not
# matter but adding a fetch, changing a URL or version, or appending `| sh`
# to a listed fetch does. Identical lines count separately (multiset). The
# snippet is for humans and is not compared. Each listed file must also appear
# in docs/security/BUILD-SUPPLY-CHAIN.md, which says how each fetch is pinned.
#
# Never allowed, even if listed: a floating `@latest`, `go install` with a
# version that is a bare variable (possible @latest), and a project-level
# `npm install` (use `npm ci`; `npm install -g <pkg>@<ver>` is a normal listed
# fetch).
#
# A line is exempt only if it names at least one URL and every URL host is
# exactly localhost, 127.0.0.1 or [::1] (a trailing comment or a host such as
# localhost.example does not qualify). Comment lines are ignored.
#
# Not scanned: scripts/ci/testdata, this script, markdown, and trees that are
# install-time or non-product (hacks/, benchmark/, examples/, terraform/,
# experimental/), plus YAML outside .github/. The doc lists them.
#
# Usage:
#   scripts/ci/check-build-fetches.sh              # check the repo
#   scripts/ci/check-build-fetches.sh --list       # print current inventory lines
#   scripts/ci/check-build-fetches.sh --self-test  # seeded fixtures must fail
# Env (used by the self-test): ROOT, ALLOW, DOC.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)

# NUL-separated files in scope (relative-path exclusions applied by in_scope).
files() {
  find "$1" \( -path '*/node_modules' -o -path '*/.git' \) -prune -o -type f \
    \( \( \( -name '*.yml' -o -name '*.yaml' \) -path '*/.github/*' \) \
       -o -name '*.sh' -o -name '*.bash' -o -name '*.mk' -o -name Makefile \
       -o -name 'Dockerfile*' \) -print0 | sort -z
}
in_scope() {
  case "$1" in
    scripts/ci/testdata/*|scripts/ci/check-build-fetches.sh) return 1 ;;
    hacks/*|benchmark/*|examples/*|terraform/*|experimental/*) return 1 ;;
  esac
}

# awk program: for one file prints
#   T<TAB>tool<TAB>normalised-segment    for each fetch
#   F<TAB>lineno<TAB>reason<TAB>text      for each forbidden construct
AWK='
function loopback_only(line,   rest, n, u, host) {
  n = 0; rest = line
  while (match(rest, /[A-Za-z][A-Za-z0-9+.-]*:\/\/[^ \t"'"'"')\\|;&]*/)) {
    u = substr(rest, RSTART, RLENGTH); rest = substr(rest, RSTART + RLENGTH)
    sub(/^[^:]*:\/\//, "", u); sub(/^[^@\/]*@/, "", u)
    if (u ~ /^\[/) { host = u; sub(/\].*$/, "]", host) } else { host = u; sub(/[:\/?#].*$/, "", host) }
    if (host != "localhost" && host != "127.0.0.1" && host != "[::1]") return 0
    n++
  }
  return n > 0
}
function seg(line, re,   s) {
