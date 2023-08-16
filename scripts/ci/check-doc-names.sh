#!/usr/bin/env bash
# Fail when published docs name a specific deployment (CLAUDE.md, "No instance /
# endpoint / tenant names in OSS-visible files").
#
# Usage:
#   scripts/ci/check-doc-names.sh [file-or-dir ...]   # default: docs README.md CHANGELOG.md
#   scripts/ci/check-doc-names.sh --self-test
#
# Rules (every *.md under the given paths):
#   public-ip   an IPv4 address outside private, loopback, link-local, multicast,
#               0.0.0.0/8 and the RFC 5737 documentation ranges, unless listed
#               as `ip` in doc-names.allow. Applies inside code fences too.
#   tailscale   an address in 100.64.0.0/10, or any hostname under .ts.net.
#   private-ip  a private IPv4 *host* address in running prose, outside a fenced
#               block. Not flagged: a CIDR (followed by /N), 10.0.3.x (the
#               Incus default bridge) and CHANGELOG.md (historical default subnets). Write <private-ip> instead.
#   tenant-name a `<word>-container` token (a single word before -container)
#               whose word is not a generic example in doc-names.allow. Tokens
#               with placeholders (<tenant>-container, ${user}-container) and
#               hyphenated words (per-container) never match.
# There is deliberately no list of real names: the rules are structural, and an
# entry in doc-names.allow needs a reason.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ALLOW=${DOC_NAMES_ALLOW:-$HERE/doc-names.allow}

scan() {
  local f
  while IFS= read -r -d '' f; do
