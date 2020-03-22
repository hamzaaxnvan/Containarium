#!/usr/bin/env bash
# Build or check the NOTICE file (#2561).
#
#   scripts/ci/notice.sh generate   write NOTICE to stdout
#   scripts/ci/notice.sh check      fail if NOTICE is missing a module that needs it
#
# Apache-2.0 section 4(d): a redistribution that includes a work with a NOTICE
# file must carry that file's attribution notices. A Go binary embeds its
# dependencies, so the NOTICE texts of every module linked into a program we
# ship belong in ours. `generate` collects them; `check` is the CI half, so a
# new dependency that brings a NOTICE of its own cannot be added without the
# file being regenerated.
#
# Same program set, and same build tag for the client CLI, as
# scripts/ci/check-licenses.sh. Needs the module cache (go mod download).
set -euo pipefail

mode="${1:?usage: notice.sh generate|check}"
cd "$(dirname "$0")/../.."

# "<module> <version> <dir>" for every non-main module linked into a shipped
# program.
modules() {
  {
    go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' \
      ./cmd/containariumd ./cmd/mcp-server ./cmd/agent-box ./cmd/model-gateway
    GOFLAGS="${GOFLAGS:-} -tags=containarium_client" \
      go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' \
      ./cmd/containarium
  } | sort -u
}

# Print "<module> <version> <notice file>" for those that carry a NOTICE.
with_notice() {
  local p v d f
  while read -r p v d; do
    [ -n "$d" ] || continue
    for f in "$d"/NOTICE "$d"/NOTICE.txt "$d"/NOTICE.md; do
      [ -f "$f" ] && echo "$p $v $f"
    done
  done < <(modules) | sort -u
}

case "$mode" in
generate)
  cat <<'HEADER'
Containarium
