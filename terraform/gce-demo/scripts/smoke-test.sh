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
      exit 64
      ;;
  esac
done

# ---- helpers ---------------------------------------------------------

CHECK_NUM=0
fail() {
  echo "  ✗ FAIL: $*" >&2
  exit 1
}

check() {
  CHECK_NUM=$((CHECK_NUM + 1))
  echo
  echo "──[$CHECK_NUM] $*"
}

ok() {
  echo "  ✓ ok"
}

tf_out() {
  terraform -chdir="$TF_DIR" output -raw "$1" 2>/dev/null \
    || fail "terraform output '$1' missing — did you run 'terraform apply' first?"
}

# curl the public API endpoint by pinning the base domain to the
# sentinel IP (DNS-independent), while still verifying the real cert.
api_curl() {
  # usage: api_curl <path> [extra curl args...]
  local path="$1"; shift
  curl -sS --max-time 20 --resolve "${BASE_DOMAIN}:443:${SENTINEL_IP}" "$@" \
    "https://${BASE_DOMAIN}${path}"
}

# ---- preflight -------------------------------------------------------

if [[ ! -f "$TF_DIR/terraform.tfstate" ]] && [[ ! -d "$TF_DIR/.terraform" ]]; then
  fail "no Terraform state in $TF_DIR — run 'terraform apply' first"
fi
command -v gcloud >/dev/null || fail "gcloud not on PATH"
command -v curl >/dev/null   || fail "curl not on PATH"

PROJECT_ID="$(tf_out project_id)"
ZONE="$(tf_out zone)"
SENTINEL_IP="$(tf_out sentinel_ip)"
SPOT_VM="$(tf_out spot_vm_name)"
BASE_DOMAIN="$(tf_out demo_base_domain)"

echo "Targeting:"
echo "  project:     $PROJECT_ID"
echo "  zone:        $ZONE"
echo "  backend:     $SPOT_VM (via IAP)"
echo "  sentinel IP: $SENTINEL_IP"
echo "  base domain: $BASE_DOMAIN"

if [[ "$BASE_DOMAIN" == \(* ]]; then
  fail "app-hosting/base_domain not configured ($BASE_DOMAIN) — set base_domain in tfvars"
fi

