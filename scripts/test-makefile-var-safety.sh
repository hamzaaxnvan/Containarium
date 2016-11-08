#!/usr/bin/env bash
# Regression test for #1732: GNU Make expands $(shell ...) (and re-scans any
# nested $(...) it produces) wherever a recipe body dereferences a variable —
# including one that arrived via a command-line override (`make build
# VERSION=...`). Worse, Make auto-exports every command-line-set variable
# into each recipe's shell environment, which forces that same expansion
# even for a target whose recipe never references the variable at all.
#
# This proves, against the real repo Makefile, that none of the documented
# override points (VERSION, GIT_COMMIT, BUILD_TIME, BUNDLE_VERSION,
# BUNDLE_OS, BUNDLE_ARCH) can execute an attacker-supplied payload, while a
# legitimate override still reaches the build as plain data.
set -uo pipefail
cd "$(dirname "$0")/.."

pass=0; fail=0
proof="$(mktemp -u)"

check_inert() { # var payload_kind payload
  local var="$1" kind="$2" payload="$3"
  rm -f "$proof"
  make version "$var=$payload" >/dev/null 2>&1
  if [ -e "$proof" ]; then
    echo "  FAIL  $var=<$kind> executed (proof file created)"
    fail=$((fail+1))
