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

