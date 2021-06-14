#!/bin/bash
#
# Stop and delete a benchmark Incus VM (including its disk), freeing the
# host's resources for the other side's run — see README.md "Why
# sequential, not parallel".
#
# Usage:
#   scripts/99-teardown-vm.sh --name <vm-name>

set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
# shellcheck source=lib.sh
source ./lib.sh

