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

