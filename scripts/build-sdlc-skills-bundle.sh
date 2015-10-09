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
