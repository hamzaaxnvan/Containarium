#!/usr/bin/env bash
# build-sdlc-skills-bundle.sh — package the pinned SDLC role skills (#2065).
#
# Reads one source section from pkg/core/sdlcskills/sdlc-skills.lock, verifies
# that namespaces do not collide across any pinned source, and writes:
#   <source>-skills.tar.gz         <ns>-<name>/SKILL.md, plus _shared/
#   <source>-skills.tar.gz.sha256  its sha256
# The tarball is deterministic (sorted entries, fixed owner/mtime, no gzip
# timestamp): the same ref always yields the same bytes, so the lock can carry
# the digest and CI can verify it by rebuilding.
#
# Usage: build-sdlc-skills-bundle.sh [--update-lock] [source] <out-dir>
#   --update-lock   write the computed sha256 back into the lock (the bump step)
#   source          lock section to build (default sdlc; the legacy flat lock
#                   is treated as a single source named sdlc)
# Env:
#   SDLC_SKILLS_LOCK      lock path (default pkg/core/sdlcskills/sdlc-skills.lock)
#   SDLC_SKILLS_REPO_URL  clone URL override for a one-source lock (tests)
#   SDLC_SKILLS_REPO_URL_<SOURCE>  per-source override, upper-case with hyphens
#                   changed to underscores (e.g. ..._DOMAIN_SKILLS)
set -euo pipefail

UPDATE_LOCK=0
POSITIONAL=()
for arg in "$@"; do
  case "$arg" in
    --update-lock) UPDATE_LOCK=1 ;;
    -*) echo "build-sdlc-skills-bundle: unknown flag: $arg" >&2; exit 2 ;;
    *) POSITIONAL+=("$arg") ;;
  esac
done
case "${#POSITIONAL[@]}" in
  1) SOURCE=sdlc; OUT="${POSITIONAL[0]}" ;;
  2) SOURCE="${POSITIONAL[0]}"; OUT="${POSITIONAL[1]}" ;;
  *) echo "usage: build-sdlc-skills-bundle.sh [--update-lock] [source] <out-dir>" >&2; exit 2 ;;
esac
[[ "$SOURCE" =~ ^[a-z0-9-]+$ ]] || { echo "build-sdlc-skills-bundle: source id '$SOURCE' must match [a-z0-9-]+" >&2; exit 2; }

LOCK="${SDLC_SKILLS_LOCK:-pkg/core/sdlcskills/sdlc-skills.lock}"
[[ -f "$LOCK" ]] || { echo "build-sdlc-skills-bundle: no lock file: $LOCK" >&2; exit 2; }

mapfile -t SOURCES < <(sed -n 's/^\[\([a-z0-9-][a-z0-9-]*\)\]$/\1/p' "$LOCK")
LEGACY=0
if [[ "${#SOURCES[@]}" -eq 0 ]]; then
  SOURCES=(sdlc)
  LEGACY=1
fi
known="$(IFS=', '; echo "${SOURCES[*]}")"
[[ " ${SOURCES[*]} " == *" $SOURCE "* ]] || { echo "build-sdlc-skills-bundle: unknown source '$SOURCE'; known sources: $known" >&2; exit 2; }

source_get() {
  local source="$1" key="$2"
  if [[ "$LEGACY" == 1 ]]; then
    sed -n "s/^${key}=//p" "$LOCK" | head -n1
  else
    awk -v section="[$source]" -v key="$key" '
      /^\[/ { active = ($0 == section); next }
      active && index($0, key "=") == 1 { sub("^[^=]*=", ""); print; exit }
    ' "$LOCK"
  fi
}

source_url() {
  local source="$1" repo="$2" env_id var override
  env_id="$(printf '%s' "$source" | tr '[:lower:]-' '[:upper:]_')"
  var="SDLC_SKILLS_REPO_URL_${env_id}"
  override="${!var:-}"
  if [[ -z "$override" && "${#SOURCES[@]}" -eq 1 ]]; then
    override="${SDLC_SKILLS_REPO_URL:-}"
  fi
  printf '%s' "${override:-https://github.com/${repo}.git}"
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
stage="$tmp/stage"
mkdir -p "$stage" "$OUT"

# Role skills: <ns>/<name>.md -> <ns>-<name>/SKILL.md. Top-level files (README,
# CONTRIBUTING) and dot-dirs are not skills; _shared/ is read at runtime by the
# skills and is copied as-is. Inspect every source even when building only one:
# otherwise two sources could silently publish the same namespace and the last
# one installed would win.
count=0
namespaces=" "
declare -A namespace_owner=()
REF=""; REPO=""; WANT_SHA=""
for source in "${SOURCES[@]}"; do
  repo="$(source_get "$source" repo)"
  ref="$(source_get "$source" ref)"
  want_sha="$(source_get "$source" sha256)"
  [[ -n "$repo" ]] || { echo "build-sdlc-skills-bundle: source '$source' has no repo" >&2; exit 2; }
  [[ "$ref" =~ ^[0-9a-f]{40}$ ]] || { echo "build-sdlc-skills-bundle: source '$source' ref must be a full 40-hex commit sha, got '$ref'" >&2; exit 2; }
  [[ -z "$want_sha" || "$want_sha" =~ ^[0-9a-f]{64}$ ]] || { echo "build-sdlc-skills-bundle: source '$source' sha256 is not a 64-hex digest" >&2; exit 2; }
  src="$tmp/src-$source"
  mkdir -p "$src"
  git -C "$src" init -q
  git -C "$src" fetch -q --depth 1 "$(source_url "$source" "$repo")" "$ref"
  git -C "$src" checkout -q --detach FETCH_HEAD
  got_ref="$(git -C "$src" rev-parse HEAD)"
  [[ "$got_ref" == "$ref" ]] || { echo "build-sdlc-skills-bundle: source '$source' fetched $got_ref, lock pins $ref" >&2; exit 1; }

  source_count=0
  for d in "$src"/*/; do
    ns="$(basename "$d")"
    [[ "$ns" == _shared ]] && continue
    if [[ -n "${namespace_owner[$ns]:-}" ]]; then
      echo "build-sdlc-skills-bundle: namespace '$ns' is present in sources '${namespace_owner[$ns]}' and '$source'" >&2
      exit 1
    fi
    namespace_owner[$ns]="$source"
    namespaces+="$ns "
    for f in "$d"*.md; do
      [[ -f "$f" ]] || continue
      source_count=$((source_count + 1))
      if [[ "$source" == "$SOURCE" ]]; then
        name="$(basename "$f" .md)"
        mkdir -p "$stage/${ns}-${name}"
        cp "$f" "$stage/${ns}-${name}/SKILL.md"
        count=$((count + 1))
      fi
    done
  done
  [[ "$source_count" -gt 0 ]] || { echo "build-sdlc-skills-bundle: source '$source' has no role skills at $ref" >&2; exit 1; }
  if [[ "$source" == "$SOURCE" ]]; then
    REF="$ref"; REPO="$repo"; WANT_SHA="$want_sha"
    if [[ -d "$src/_shared" ]]; then
      mkdir -p "$stage/_shared"
      cp "$src/_shared/"*.md "$stage/_shared/"
    fi
  fi
done
[[ "$count" -gt 0 ]] || { echo "build-sdlc-skills-bundle: source '$SOURCE' has no role skills at $REF" >&2; exit 1; }
# Skill sets (#3048): the lock's `set.<id>=<ns>,<ns>` lines say which namespaces
# each set id selects (install-sdlc-skills.sh SKILL_SETS). Once a lock declares
# any, they must partition the namespaces in the archive: every namespace in
# exactly one set, no set naming a namespace that is not there. A new upstream
# namespace therefore fails the build until it is assigned, instead of being
# silently unselectable.
if grep -q '^set\.' "$LOCK"; then
  assigned=" "
  while IFS='=' read -r key val; do
    id="${key#set.}"
    [[ "$id" =~ ^[a-z0-9-]+$ ]] || { echo "build-sdlc-skills-bundle: bad set id '$id' in the lock (want [a-z0-9-]+)" >&2; exit 1; }
    IFS=',' read -r -a nss <<< "$val"
    [[ "${#nss[@]}" -gt 0 ]] || { echo "build-sdlc-skills-bundle: set '$id' has no namespaces" >&2; exit 1; }
    for ns in "${nss[@]}"; do
      [[ "$namespaces" == *" $ns "* ]] || { echo "build-sdlc-skills-bundle: set '$id' names namespace '$ns', which is not in $REF" >&2; exit 1; }
      [[ "$assigned" != *" $ns "* ]] || { echo "build-sdlc-skills-bundle: namespace '$ns' is in more than one set" >&2; exit 1; }
      assigned+="$ns "
    done
  done < <(grep '^set\.' "$LOCK")
  for ns in $namespaces; do
    [[ "$assigned" == *" $ns "* ]] || { echo "build-sdlc-skills-bundle: namespace '$ns' is in no set; add it to a set.<id> line in the lock" >&2; exit 1; }
  done
fi
ASSET="${SOURCE}-skills.tar.gz"
python3 -I - "$stage" "$OUT/$ASSET" <<'PY'
import gzip, io, os, sys, tarfile
stage, out = sys.argv[1], sys.argv[2]
names = []
for root, dirs, files in os.walk(stage):
    dirs.sort()
    for n in sorted(files):
