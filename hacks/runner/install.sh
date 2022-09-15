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

# Runner groups exist only at organization scope (#1217). Refuse a group on a
# repository target here, before anything is installed, rather than letting
# config.sh fail inside the respawn loop.
RUNNER_GROUP="${RUNNER_GROUP:-}"
case "$GH_REPO" in
  */*)
    if [ -n "$RUNNER_GROUP" ]; then
      echo "RUNNER_GROUP is set but GH_REPO=${GH_REPO} is a repository; runner groups need an organization target (a bare owner)" >&2
      exit 1
    fi
    ;;
esac

# GH_BASE_URL — the GitHub server URL. Defaults to github.com; set to
# your GHES URL for on-prem deployments (e.g.
# https://github.your-company.internal). Added per
# prd/cloud/air-gapped-install-bundle.md (E3a §"GHES support").
GH_BASE_URL="${GH_BASE_URL:-https://github.com}"

# Strip any trailing slash so downstream concatenation is consistent
# regardless of whether the operator typed it with or without.
GH_BASE_URL="${GH_BASE_URL%/}"

# Derive the API base URL. github.com hosts the API at the separate
# api.github.com hostname; GHES collapses both under one host at /api/v3.
if [ "$GH_BASE_URL" = "https://github.com" ]; then
  GH_API_BASE="https://api.github.com"
else
  GH_API_BASE="${GH_BASE_URL}/api/v3"
fi

RUNNER_NAME="${RUNNER_NAME:-$(hostname -s)}"
RUNNER_LABELS="${RUNNER_LABELS:-containarium,ephemeral}"
RUNNER_VERSION="${RUNNER_VERSION:-2.319.1}"
RUNNER_USER="${RUNNER_USER:-ghrunner}"
RUNNER_HOME="${RUNNER_HOME:-/opt/actions-runner}"

RUNNER_DNS="${RUNNER_DNS:-}"

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) RUNNER_ARCH=x64 ;;
  aarch64|arm64) RUNNER_ARCH=arm64 ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

# ---- optional: pin the box resolver (RUNNER_DNS) ----
# Some environments hand the box a split-horizon / internal DNS record for a
# host it actually has to reach over public egress — e.g. a Containarium
# tenant box whose bridge resolver maps cloud.containarium.dev to a private
# control-plane IP that goes stale after a control-plane redeploy, so
# `containarium create` fails with curl exit 7 ("couldn't connect"). Pinning
# a public resolver makes the box resolve such hosts to their public address
# and reach them over its normal egress. Done before anything network-heavy
# so the whole install uses the intended resolver. No-op when RUNNER_DNS is
# unset (the right default for GHES / air-gapped runners).
if [ -n "$RUNNER_DNS" ]; then
  if systemctl is-active --quiet systemd-resolved 2>/dev/null; then
    mkdir -p /etc/systemd/resolved.conf.d
    # Domains=~. routes ALL lookups to these servers, overriding the
    # per-link DNS that DHCP set from the bridge.
    cat > /etc/systemd/resolved.conf.d/zz-containarium-runner-dns.conf <<EOF
[Resolve]
DNS=${RUNNER_DNS}
Domains=~.
EOF
    systemctl restart systemd-resolved
  else
    # No systemd-resolved (stub at 127.0.0.53) — write resolv.conf directly,
    # replacing any managed symlink so our entries actually take effect.
    rm -f /etc/resolv.conf
    : > /etc/resolv.conf
    for ns in $RUNNER_DNS; do echo "nameserver ${ns}" >> /etc/resolv.conf; done
  fi
  echo "Pinned box resolver to: ${RUNNER_DNS}"
fi

# ---- system deps ----
# `sudo` is required so the runner user can be granted passwordless sudo
# (below): both FootprintAI/containarium-run (its `install-cli.sh | sudo
# bash` step) and a workflow's own dependency-install steps shell out to
# sudo, and a bare ubuntu/24.04 box may not ship it. We deliberately do NOT
# pre-install repo-specific job tools (rsync, yq, ...) here — once the runner
# user has sudo, the *workflow* installs whatever it needs, keeping the
# runner image generic and repo-agnostic. jq stays because the respawn loop
# itself needs it (to parse the registration-token response) before any job.
apt-get update
apt-get install -y --no-install-recommends \
  ca-certificates curl jq git build-essential libicu-dev sudo

# ---- toolchains ----
# Go (matches Containarium repo's go.mod pinning convention; bumps are
# a one-line edit here).
GO_VERSION="${GO_VERSION:-1.25.10}"
if ! command -v go >/dev/null 2>&1 || [ "$(go version | awk '{print $3}')" != "go${GO_VERSION}" ]; then
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz
  ln -sf /usr/local/go/bin/go /usr/local/bin/go
  ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
fi

# Node 22 + pnpm 9 (for repos with frontend builds)
if ! command -v node >/dev/null 2>&1; then
  curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
  apt-get install -y nodejs
  npm install -g pnpm@9
