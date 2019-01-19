#!/usr/bin/env bash
# Sign one pushed container image by digest with cosign keyless (GitHub OIDC)
# and verify the signature straight away.
#
# Usage: scripts/release/sign-image.sh <image> <digest> <workflow-file> [evidence-dir]
#
#   image          repository without tag or digest, e.g. ghcr.io/<org>/<name>
#   digest         the digest the push produced (sha256:<64 hex>), normally
#                  docker/build-push-action's `digest` output. With provenance
#                  and SBOM attestations on, that is the image index digest.
#   workflow-file  the workflow file expected to be signing, e.g. sidecars.yml
#   evidence-dir   where to write <name>.json (what was signed) and
#                  <name>.verify.json (cosign verify output); default: ./image-signatures
#
# Signs <image>@<digest>, never a tag: a tag can be moved after signing, a
# digest cannot. The verify step pins the same identity a user is told to
# check (docs/security/VERIFYING-RELEASES.md), so an image signed with the
# wrong identity fails here.
#
# Environment:
#   COSIGN       path to cosign (default: cosign on PATH)
#   OIDC_ISSUER  the certificate issuer (default: GitHub Actions)
#   GITHUB_REPOSITORY, GITHUB_REF, GITHUB_WORKFLOW_REF  set by GitHub Actions
set -euo pipefail

image="${1:?usage: sign-image.sh <image> <digest> <workflow-file> [evidence-dir]}"
