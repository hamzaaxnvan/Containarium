#!/usr/bin/env bash
#
# Backend de-risk for Tier 3 PR-1 (#662): prove TPROXY steering + original-dst
# recovery + forward works end to end, with the daemon's transparent proxy.
#
# Runs ENTIRELY INSIDE A THROWAWAY NETWORK NAMESPACE so it does NOT touch the
# host's nft ruleset, routing, or live traffic — create netns, set up tproxy +
# the proxy + a test upstream inside it, test, destroy the netns. Zero blast
# radius on a live multi-tenant host (unlike applying tproxy rules to the host).
#
# What it proves: a client connecting to an ORIGINAL destination (a test upstream
# on 10.99.0.2:8080) is transparently steered by the nft tproxy rule to the
# proxy, which recovers that original dst (via IP_TRANSPARENT's LocalAddr) and
# forwards — the client gets the upstream's response back. This is the PR-1
# acceptance: steer→recover→forward, no WAF yet.
#
# Requires: root (netns + nft + IP_TRANSPARENT), nft_tproxy module (host-wide),
# and a built linux `waf-tproxy` probe binary (see BUILD below). Anonymise output.
#
# Usage (on the backend, as root):
#   sudo BUILD=1 ./validate-tproxy-steering.sh        # build the probe + run
#   sudo PROBE=/path/to/waf-tproxy ./validate-tproxy-steering.sh
#
set -euo pipefail

NS="waf-tproxy-test-$$"
TPROXY_PORT="${TPROXY_PORT:-15001}"
UPSTREAM_IP="10.99.0.2"
UPSTREAM_PORT=8080
PROBE="${PROBE:-/tmp/waf-tproxy}"

PASS=0 FAIL=0
pass() { printf '  \033[32mPASS\033[0m %s\n' "$*"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAIL=$((FAIL + 1)); }
