#!/bin/bash
# install.sh — provisions a Containarium box as an ephemeral GitHub
# Actions self-hosted runner for one GitHub repo.
#
# Run this INSIDE a Containarium box (e.g. one created via
# `containarium create runner-1`). It installs:
#
#   - actions-runner (GitHub's official binary)
#   - Common CI toolchains: git, build-essential, Go, Node 22, pnpm 9,
#     buf 1.38.0, golangci-lint
#   - A systemd unit that runs the runner in `--ephemeral` mode in a
#     respawn loop: register → run ONE job → exit → restart fresh.
#
# Required env vars (or pass on command line):
#
#   GH_REPO     registration target: owner/repo for one repository, or a
#               bare owner for an ORGANIZATION runner usable by every repo
#               in it (#1217)
#   GH_PAT      personal access token with `repo` scope; used to mint
#               short-lived runner-registration tokens at the start of
#               each loop iteration. Pick from
#               ${GH_BASE_URL}/settings/tokens (classic) or App
#               installation tokens.
#   GH_BASE_URL (optional) base URL of the GitHub server. Defaults to
#               https://github.com. Set this to your GHES URL for
#               GitHub Enterprise Server deployments, e.g.
#               https://github.your-company.internal. The API URL is
#               derived from this (api.github.com for github.com,
#               ${GH_BASE_URL}/api/v3 for GHES). See
#               docs/GHES.md inside the air-gapped install bundle.
#   RUNNER_NAME (optional) name for this runner; defaults to the box
#               hostname. Visible in GitHub's Actions → Runners list.
#   RUNNER_LABELS (optional) comma-separated runner labels; defaults
#               to "containarium,ephemeral". Workflows target with
#               `runs-on: [self-hosted, containarium, ephemeral]`.
#   RUNNER_GROUP (optional) organization runner group to register into;
#               defaults to the org's default group. Organization targets
#               only (a bare owner in GH_REPO).
#   RUNNER_DNS  (optional) space-separated DNS server(s) to pin the box's
#               resolver to, e.g. "8.8.8.8 8.8.4.4". Use when the box is
#               handed a split-horizon / internal record for a host it
#               must reach over public egress (e.g. a tenant box whose
#               bridge DNS resolves the control plane to a stale internal
#               IP). Unset (default) leaves the box's DNS untouched — the
#               correct default for GHES / air-gapped runners that rely on
#               their own internal resolver.
#
# Usage from outside the box:
#
#   containarium create runner-1
#   ssh runner-1 'curl -fsSL https://raw.githubusercontent.com/footprintai/containarium/main/hacks/runner/install.sh \
#     | sudo GH_REPO=FootprintAI/Containarium-cloud GH_PAT=ghp_xxx bash'
#
# Against GitHub Enterprise Server:
#
#   ssh runner-1 'sudo \
#     GH_REPO=org/repo \
#     GH_PAT=ghp_xxx \
#     GH_BASE_URL=https://github.your-company.internal \
#     ./hacks/runner/install.sh'
#
# After this runs, the runner registers itself within ~30s and starts
# accepting jobs. Verify at
# https://github.com/<owner>/<repo>/settings/actions/runners.

set -euo pipefail

# ---- input validation ----
: "${GH_REPO:?GH_REPO is required (owner/repo, or a bare owner for an org runner)}"
: "${GH_PAT:?GH_PAT is required (PAT with repo scope, or admin:org for an org runner)}"
