# containarium-sshpiper
#
# The SSH gateway image for the Kubernetes backend: sshpiperd with its
# bundled plugins, from the SAME pinned upstream release the sentinel
# installs (terraform startup-sentinel.sh pins SSHPIPER_VERSION) — one
# canonical gateway version across the LXC and K8s worlds, published under
# our registry so the chart never depends on a third-party :latest tag
# (Containarium#976).
#
# Published per-release as ghcr.io/footprintai/containarium-sshpiper (see
# .github/workflows/sidecars.yml). Build:
#   docker build -f images/sshpiper/Dockerfile -t containarium-sshpiper images/sshpiper
#
# Runtime contract (drop-in for the deployment shape the chart/example use):
#   - PLUGIN selects the routing plugin (default: kubernetes)
#   - SSHPIPERD_* env flows through to sshpiperd (SERVER_KEY, LOG_LEVEL, ...)
#   - listens on :2222, runs as non-root uid 1000
#
# Built from source rather than downloading upstream's release binary
# (containarium#2126): sshpiperd_with_plugins_linux_x86_64.tar.gz is built
# once, at release time, against whatever dependency versions were current
# then — v1.6.1's own binary still embeds a vulnerable google.golang.org/grpc
# (v1.82.1) and was compiled with an outdated Go toolchain, so several real
# CRITICAL/HIGH CVEs with published fixes ship in the tarball regardless of
# which SSHPIPER_VERSION is pinned, and this image's own Trivy scan (added
# in the same PR) blocks the build on exactly that. Building from the tagged
# source with a current Go toolchain, plus a targeted grpc pin below, closes
# that gap without waiting on an upstream rebuild.

FROM golang:1.26.10-bookworm AS build
ARG SSHPIPER_VERSION=v1.6.1
ENV CGO_ENABLED=0
RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
# cmd/sshpiperd is its OWN nested Go module (has its own go.mod, no semver
# tags of its own — goreleaser builds it from a plain checkout via
# `dir: ./cmd/sshpiperd`, per upstream's .goreleaser.yaml) so it isn't
# reachable through `go get .../cmd/sshpiperd@${SSHPIPER_VERSION}`; clone
# the tag like upstream's own release process does.
RUN git clone --depth 1 --branch "${SSHPIPER_VERSION}" https://github.com/tg123/sshpiper .

# Both this repo's root module and cmd/sshpiperd's nested module pin grpc
