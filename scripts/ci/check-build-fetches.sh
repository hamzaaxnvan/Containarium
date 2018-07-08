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
  if (!match(line, re)) return ""
  s = substr(line, RSTART)
  if (s !~ /^[A-Za-z]/) s = substr(s, 2)
  gsub(/[ \t]+/, " ", s); sub(/ $/, "", s)
  return s
}
BEGIN {
  W = "(^|[^A-Za-z0-9_.-])"; T = "([ \t]|$)"
  n = split("curl|wget|go install|go get|npm install|npm ci|pip install|pipx|uv|npx|git clone|docker pull|cargo install|gem install|xcaddy build|ADD url", names, "|")
  re["curl"] = W "curl" T
  re["wget"] = W "wget" T
  re["go install"] = W "go[ \t]+install" T
  re["go get"] = W "go[ \t]+get" T
  re["npm install"] = W "(npm|pnpm|yarn)[ \t]+(install|i|add)" T
  re["npm ci"] = W "npm[ \t]+ci" T
  re["pip install"] = W "pip3?[ \t]+install" T
  re["pipx"] = W "pipx[ \t]+(install|run|inject)" T
  re["uv"] = W "uv[ \t]+(pip|tool|run|add|sync)" T
  re["npx"] = W "npx" T
  re["git clone"] = W "git[ \t]+clone" T
  re["docker pull"] = W "docker[ \t]+pull" T
  re["cargo install"] = W "cargo[ \t]+install" T
  re["gem install"] = W "gem[ \t]+install" T
  re["xcaddy build"] = W "xcaddy\"?[ \t]+build" T
  re["ADD url"] = "^[ \t]*ADD[ \t]+(--[^ \t]+[ \t]+)*https?://"
}
{
  if ($0 ~ /^[ \t]*#/) next
  start = NR; line = $0
  while (line ~ /\\$/ && (getline nxt) > 0) { sub(/\\$/, "", line); line = line nxt }
  if (line ~ /@latest([^A-Za-z0-9_]|$)/) print "F\t" start "\t@latest\t" line
  if (line ~ /go[ \t]+install[^|;&]*@\$/) print "F\t" start "\tgo install with a variable version (possible @latest)\t" line
  if (line ~ W "npm[ \t]+(install|i)" T && line !~ /[ \t](-g|--global)([ \t]|$)/) print "F\t" start "\tnpm install (use npm ci)\t" line
  if (loopback_only(line)) next
  for (i = 1; i <= n; i++) {
    s = seg(line, re[names[i]])
    if (s != "") print "T\t" names[i] "\t" s
  }
}'

fp() {
  local out
  if command -v sha256sum >/dev/null; then out=$(printf '%s' "$1" | sha256sum); else out=$(printf '%s' "$1" | shasum -a 256); fi
  echo "${out:0:12}"
}

# inventory <root>: sorted "path<TAB>tool<TAB>fp<TAB>snippet" for every fetch;
# forbidden <root>: "path:line: reason: text".
run_scan() {
  local root=$1 mode=$2 file rel kind a b c
  while IFS= read -r -d '' file; do
    rel=${file#"$root"/}
    in_scope "$rel" || continue
    while IFS=$'\t' read -r kind a b c; do
      if [[ "$mode" == inventory && "$kind" == T ]]; then
        printf '%s\t%s\t%s\t%s\n' "$rel" "$a" "$(fp "$b")" "${b:0:70}"
      elif [[ "$mode" == forbidden && "$kind" == F ]]; then
        printf '%s:%s: %s: %s\n' "$rel" "$a" "$b" "$c"
      fi
    done < <(awk "$AWK" "$file")
  done < <(files "$root")
}

keys() { cut -f1-3 | sort; }   # multiset key: path, tool, fingerprint

check() {
  local root=$1 allow=$2 doc=$3 bad=0 forb found listed path rest line
  forb=$(run_scan "$root" forbidden)
  if [[ -n "$forb" ]]; then
    while IFS= read -r line; do echo "FORBIDDEN: $line"; done <<<"$forb"
    bad=1
  fi
  found=$(run_scan "$root" inventory)
