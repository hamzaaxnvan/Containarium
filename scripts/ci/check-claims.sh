#!/usr/bin/env bash
# Fail when README prose makes a security claim that docs/security/CLAIMS.md
# does not cover (#2558).
#
# Usage:
#   scripts/ci/check-claims.sh [README.md [CLAIMS.md]]
#   scripts/ci/check-claims.sh --self-test
#
# A "claim" is a paragraph, list item or table row of the README (outside
# fenced code) that contains a phrase from scripts/ci/claim-phrases.txt.
# A claim is covered when its heading section either
#   - links to docs/security/CLAIMS.md (the section's claims are then read
#     against that table), or
#   - carries `<!-- claim-row: <text> -->` where <text> appears verbatim in
#     CLAIMS.md (the marker is how a single claim names its row), or
#   - carries `<!-- claim-exempt: <reason> -->` (at least 15 characters) for
#     text that only mentions a phrase without asserting it, such as advice
#     or a description of another product. Each one is visible in review.
# Sections without either, and markers naming no row, fail. CLAIMS.md itself
# must keep every table row's status cell in bold, so a row cannot be a bare
# sentence.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PHRASES=${CLAIM_PHRASES:-$HERE/claim-phrases.txt}

phrase_re() { grep -vE '^[[:space:]]*(#|$)' "$PHRASES" | paste -sd'|' -; }

check() {
  local readme=$1 claims=$2 re bad=0 out
  re=$(phrase_re)
  out=$(awk -v re="($re)" '
    function flush(   i) {
      if (sec_hit != "" && !sec_cov) { print "UNBACKED: " FILENAME ":" hit_line ": " sec_hit; }
    }
    function endblock() {
      if (blk != "") {
        if (blk ~ /CLAIMS\.md/) sec_cov = 1
        if (match(tolower(blk), re) && sec_hit == "") {
          sec_hit = substr(blk, 1, 100); hit_line = blk_line
        }
        while (match(blk, /<!-- *claim-(row|exempt):[^>]*-->/)) {
          m = substr(blk, RSTART, RLENGTH); blk = substr(blk, RSTART + RLENGTH)
          kind = (m ~ /claim-exempt/) ? "EXEMPT" : "MARKER"
          sub(/^<!-- *claim-(row|exempt): */, "", m); sub(/ *-->$/, "", m)
          print kind "\t" m "\t" blk_line
          sec_cov = 1
        }
      }
      blk = ""
    }
    /^[ \t]*(```|~~~)/ { endblock(); fence = !fence; next }
    fence { next }
    /^#+ / { endblock(); flush(); sec_hit = ""; sec_cov = 0; next }
    /^[ \t]*$/ { endblock(); next }
    /^[ \t]*([-*]|[0-9]+\.)[ \t]/ || /^[ \t]*\|/ { endblock() }
    { if (blk == "") blk_line = FNR; blk = blk " " $0 }
    END { endblock(); flush() }
  ' "$readme" || true)
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    if [[ "$line" == EXEMPT$'\t'* ]]; then
      local why=${line#EXEMPT$'\t'}; why=${why%$'\t'*}
      if (( ${#why} < 15 )); then echo "BAD EXEMPT: $readme: claim-exempt needs a reason of at least 15 characters, got \"$why\""; bad=1; fi
    elif [[ "$line" == MARKER$'\t'* ]]; then
      local text=${line#MARKER$'\t'}; text=${text%$'\t'*}
      if ! grep -qF -- "$text" "$claims"; then echo "NO ROW: $readme: claim-row marker \"$text\" is not in $claims"; bad=1; fi
    else
      echo "$line  (no link to CLAIMS.md and no claim-row marker in its section)"; bad=1
    fi
  done <<<"$out"
  # Every table row in CLAIMS.md needs a bold status cell.
  local n
  n=$(awk -F'|' '/^\| / && $2 !~ /^ *(Claim|---)/ && $3 !~ /^ *\*\*/ { print FNR ": " $2 }' "$claims")
  if [[ -n "$n" ]]; then echo "NO STATUS in $claims:"; echo "$n"; bad=1; fi
  return "$bad"
}

self_test() {
  local d rc=0; d=$(mktemp -d); trap 'rm -rf "$d"' RETURN
  cat >"$d/CLAIMS.md" <<'Y'
| Claim | Status | Evidence |
| --- | --- | --- |
| Tenant secrets use KMS envelope encryption. | **Opt-in** | x |
Y
  cat >"$d/bare.md" <<'Y'
# Product

## Security

All traffic between tenants is zero-trust and fully isolated.
Y
  cat >"$d/linked.md" <<'Y'
# Product

## Security

All traffic between tenants is isolated. See [claims](docs/security/CLAIMS.md).
Y
  cat >"$d/marked.md" <<'Y'
## Security

Secrets are encrypted at rest. <!-- claim-row: Tenant secrets use KMS envelope encryption. -->
Y
  cat >"$d/badmarker.md" <<'Y'
## Security

Secrets are encrypted at rest. <!-- claim-row: A row that does not exist -->
Y
  cat >"$d/plain.md" <<'Y'
## Install

