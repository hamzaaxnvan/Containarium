# containarium-agent-box
#
# The in-the-box image for the Kubernetes backend (docs/K8S-AGENT-BOX-RUNTIME-DESIGN.md):
# a minimal SSH server whose every session is pinned by forced command to
# `agent-box` (the in-the-box MCP — shell/file ops over stdio). An agent reaches
# it over SSH and can do nothing but talk to agent-box.
#
# Hardening posture (matches the StatefulSet securityContext the daemon sets, so
# the pod satisfies the `restricted` Pod Security profile):
#   - runs as a non-root user (uid 1000)
#   - SSH listens on :2222, an UNPRIVILEGED port — so no capabilities are
#     needed at all (the agent connects to the gateway on :22; :2222 is the
#     internal sshpiper→pod hop).
#   - dropbear instead of OpenSSH: it runs cleanly rootless and supports a
#     forced command, so there's no sshd privsep/PAM/root dance.
#
# Published per-release as ghcr.io/footprintai/containarium-agent-box:vX.Y.Z
# by .github/workflows/sidecars.yml (release tags).
# Build context is the repo root:
#   docker build -f images/agent-box/Dockerfile -t containarium-agent-box .

# --- build agent-box -------------------------------------------------------
# No `k8s` build tag here: agent-box is the in-box MCP and never imports
# client-go, so this stays a small static binary.
FROM golang:1.26.10-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/agent-box ./cmd/agent-box

# --- runtime: rootless dropbear + agent-box --------------------------------
FROM debian:bookworm-slim
# apt-get upgrade: picks up security patches (e.g. DLA-4792-1/tzdata) that
# have shipped since debian:bookworm-slim's last rebuild but aren't pulled in
# by installing dropbear-bin/ca-certificates alone — caught by the
# agent-box-image.yml Trivy scan (ignore-unfixed still failed on this one
# because a fix was actually available).
RUN apt-get update \
    && apt-get upgrade -y \
