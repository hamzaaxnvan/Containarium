#!/usr/bin/env bash
# Licence gate and attribution file for the npm dependencies we ship (#2754).
#
#   scripts/ci/npm-licenses.sh check      fail on a disallowed licence, or when
#                                         the web UI's attribution file is stale
#   scripts/ci/npm-licenses.sh generate   write web-ui/THIRD-PARTY-LICENSES.txt
#
# Covers production dependencies only, for the two npm projects:
#   web-ui         built to a static site and embedded in the daemon. Its bundle
#                  carries none of its packages' copyright notices, so the
#                  generated file supplies them (MIT and BSD ask for the notice
#                  to travel with copies).
#   agent-runtime  its release tarball holds our compiled code plus package.json
#                  and the lockfile; the packages themselves are installed from
#                  npm inside the customer's box. Gated, not redistributed.
#
# Same allow-list as the Go check (scripts/ci/check-licenses.sh), plus 0BSD,
# which is as permissive as ISC. Needs node 20+, npm and python3.
set -euo pipefail
cd "$(dirname "$0")/../.."

mode="${1:?usage: npm-licenses.sh check|generate}"
CHECKER="license-checker-rseidelsohn@5.0.1"
OUT="web-ui/THIRD-PARTY-LICENSES.txt"
ALLOWED="Apache-2.0 MIT BSD-2-Clause BSD-3-Clause ISC Unlicense MPL-2.0 0BSD"

# Packages that are in the installed production tree but not allowed by the
# list, each with why that is acceptable. Matched by name prefix.
#   web-ui:        @img/sharp-libvips-*  LGPL-3.0. Optional native binaries of
#                  Next's image optimiser. The site is a static export with
#                  unoptimised images (checked below), so none reach the files
#                  we embed and serve.
#                  caniuse-lite  CC-BY-4.0 browser-support data read by the
#                  build tools; not part of the output.
#   agent-runtime: @anthropic-ai/claude-agent-sdk*  Anthropic's own SDK under
#                  its commercial terms (not an open-source licence). We do not
