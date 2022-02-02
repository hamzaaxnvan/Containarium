#!/usr/bin/env bash
# Shared by release.yml and the PR lane: build, inventory and package the same bytes.
set -euo pipefail
release=${1:?release id}
arch=${2:?amd64 or arm64}
tree="dist/toolchains-$arch"
