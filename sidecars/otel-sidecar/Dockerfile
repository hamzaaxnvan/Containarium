# containarium-otel-sidecar
#
# Per docs/PLATFORM-SIDECAR-DESIGN.md + docs/OTEL-AGENT-RELAY-DESIGN.md.
# Thin wrapper over a purpose-built collector with a fail-closed entrypoint
# that validates Containarium identity env vars before starting the
# collector. The upstream otel/opentelemetry-collector-contrib image
# is distroless (no shell), so we build on a slim Debian base — with a
# /bin/sh for the entrypoint script.
#
# The collector is compiled here from builder-manifest.yaml (only the
# components config.yaml uses) instead of downloading the upstream
# otelcol-contrib release tarball, whose stdlib and x/net are frozen at
# upstream's build time and failed the v0.101.0 Trivy gate with no fixed
# upstream release to move to.
#
# Image versions track the Containarium project release version per
# platform-sidecar decision #4: each Containarium release publishes
# matching tags ghcr.io/footprintai/containarium-otel-sidecar:vX.Y.Z.

FROM golang:1.26.10-bookworm AS build
# The image's Go is the toolchain the binary is built with; never let go.mod
# pull a different one (the stdlib CVE fixes depend on it).
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
# Same version as builder-manifest.yaml's otelcol_version.
ARG OTELCOL_VERSION=0.162.0
RUN go install go.opentelemetry.io/collector/cmd/builder@v${OTELCOL_VERSION}
COPY builder-manifest.yaml /build/builder-manifest.yaml
RUN builder --config /build/builder-manifest.yaml

