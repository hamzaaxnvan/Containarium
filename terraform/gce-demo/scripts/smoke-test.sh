#!/usr/bin/env bash
# Smoke-test a freshly-applied terraform/gce-demo cluster.
#
# Run AFTER `terraform apply`. Verifies four things in order:
#
#   1. The containarium daemon is running on the backend at the
#      expected version.
#   2. The public HTTPS endpoint serves a valid cert and the API
#      responds (401 without a token) — i.e. DNS-less reachability of
#      sentinel :443 -> Caddy -> backend daemon, with a real cert.
#   3. A freshly-issued JWT is minted on the backend.
#   4. That JWT authenticates against /v1/containers over HTTPS and the
#      API returns a sensible list.
#
# Architecture notes (why this script does NOT just `ssh` the sentinel):
#   - The sentinel's port 22 is sshpiper (the tenant SSH proxy), not a
#     host shell; its management sshd is on :2222 and firewalled to the
#     VPC. So the daemon binary + jwt.secret are operated on the BACKEND
#     over IAP, not on the sentinel.
#   - The daemon API (:8080) is NOT exposed publicly. It's reached
#     through the sentinel's Caddy over HTTPS at the base domain.
#
# Connections to the public endpoint use `curl --resolve` so the check
# works the moment `terraform apply` finishes — before DNS propagates —
# while still validating the real Let's Encrypt cert (SNI = base domain).
#
# Exit 0 if every check passes; 1 on the first failure (the rest are
# typically already broken once an early check fails). Run from
# terraform/gce-demo/ or pass --tf-dir=<path>.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TF_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
EXPECTED_VERSION="${EXPECTED_VERSION:-}"  # leave empty to skip version assertion

# ---- argument parsing -----------------------------------------------

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tf-dir=*)
      TF_DIR="${1#--tf-dir=}"
      shift
      ;;
    --expected-version=*)
      EXPECTED_VERSION="${1#--expected-version=}"
      shift
      ;;
    -h|--help)
      cat <<EOF
Usage: smoke-test.sh [--tf-dir=PATH] [--expected-version=X.Y.Z]

  --tf-dir=PATH         Terraform working directory (default: parent of this script).
  --expected-version    If set, fail when 'containarium version' on the backend
                        doesn't match. Useful in CI; skip locally.

Run AFTER 'terraform apply' has produced state in --tf-dir.
EOF
      exit 0
      ;;
    *)
      echo "unknown arg: $1" >&2
