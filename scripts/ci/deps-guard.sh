#!/usr/bin/env bash
# Dependency-change gate (#2794, story 3 of #2649).
#
# Usage:
#   scripts/ci/deps-guard.sh [--base <git-ref>] [--report <file>]
#   scripts/ci/deps-guard.sh --self-test
#
# Default mode fails on:
#   - a Go environment that bypasses the checksum database (GONOSUMDB,
#     GOPRIVATE, GONOPROXY or GOINSECURE set; GOSUMDB not sum.golang.org;
#     GOFLAGS without -mod=readonly);
#   - a new or changed `replace` in go.mod compared with the base (an
#     existing, unchanged replace passes);
#   - a new or re-versioned module that sum.golang.org has no record of, or
#     whose go.sum hash disagrees with it;
#   - a go.sum that lists one module version twice with different hashes;
#   - a tracked second go.mod, go.work / go.work.sum or vendor/modules.txt,
#     which this gate does not read.
# The base defaults to HEAD^1: on a pull_request checkout HEAD is GitHub's
# merge commit, whose first parent is the base branch tip. Hard fail, no
# label; the only override is a code-owner approval of the PR (decision 4 on
# #2649). The workflow runs this on PRs only (see deps-guard.yml).
#
# Trust: CI runs the PR's own copy of this script, so it cannot protect
# itself against a PR that edits it; code-owner review of scripts/** and
# .github/** (#2792) does. Its report is advisory.
#
# --self-test proves the gate can fail: every fixture under
# internal/depsguard/testdata is run against the live sum.golang.org and
# must exit with the status its `expect` file names, failing fixtures for
# the named rule only; seeded unsafe environments and a seeded second go.mod
