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
    awk -v allow="$ALLOW" -v file="$f" '
      BEGIN {
        while ((getline l < allow) > 0) {
          sub(/[ \t]+#.*$/, "", l)
          n = split(l, p, /[ \t]+/)
          if (p[1] == "ip") okip[p[2]] = 1
          else if (p[1] == "name") okname[p[2]] = 1
          else if (p[1] == "pending") pend[p[2] SUBSEP p[3]] = 1
        }
        rel = file; sub(/^\.\//, "", rel)
      }
      /^[ \t]*(```|~~~)/ { fence = !fence; next }
      {
        line = $0; rest = line
        while (match(rest, /[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/)) {
          s = RSTART; len = RLENGTH; ip = substr(rest, s, len)
          pre = (s > 1) ? substr(rest, s - 1, 1) : ""
          post = substr(rest, s + len, 1)
          after = substr(rest, s + len)
          rest = substr(rest, s + len)
          if (pre ~ /[0-9.]/ || post ~ /[0-9]/) continue
          split(ip, o, ".")
          if (o[1] > 255 || o[2] > 255 || o[3] > 255 || o[4] > 255) continue
          a = o[1] + 0; b = o[2] + 0; c = o[3] + 0
          cidr = (post == "/")
          if (a == 100 && b >= 64 && b <= 127) { print "tailscale " rel ":" FNR ": " ip; continue }
          priv = (a == 10 || (a == 172 && b >= 16 && b <= 31) || (a == 192 && b == 168))
          if (priv) {
            if (!fence && !cidr && rel != "CHANGELOG.md" && !(a == 10 && b == 0 && c == 3)) print "private-ip " rel ":" FNR ": " ip
            continue
          }
