#!/bin/bash
#
# Deploy containarium binary to all instances.
#
# Usage: ./scripts/deploy-binary.sh [--build]
#
# This script:
#   1. Optionally builds the linux binary (--build)
#   2. Uploads to the sentinel (which serves it to peers)
#   3. Deploys on the primary GCE VM
#   4. Triggers each peer to self-update from the sentinel
#
# Prerequisites:
#   - gcloud configured with access to your GCP project
#   - SSH access to your peer node hostnames
#
# Edit the constants below (PROJECT / ZONE / PRIMARY_VM / SENTINEL_VM /
# PEERS) for your deployment OR override at invocation time via env
# vars (`PROJECT=my-prod ZONE=us-east1-a ... bash deploy-binary.sh`).
#
# The systemd unit names are also overridable: most hosts run the daemon
# as `containarium` / the sentinel as `containarium-sentinel`, but some run
# the daemon under a different unit (e.g. `containarium-daemon`). Pass
# PRIMARY_SERVICE / SENTINEL_SERVICE to match — if the unit name is wrong
# the `stop` is a no-op, the running daemon keeps the binary open, and the
# `cp` fails with "Text file busy".
#
# This script only swaps the binary. It never touches the daemon's persisted
# configuration (`daemon_config`, including `base_domain`). For bringing up a
# new BYOC backend, and the checks to run after an upgrade, see
# docs/BYOC-BACKEND-BRINGUP.md.
#

set -euo pipefail

# shellcheck source=lib/deploy-install.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/deploy-install.sh"

# Reject bad HEALTH_TRIES / HEALTH_INTERVAL before any build, scp or ssh
# (deploy_install_cmd validates again when it builds the install command).
if ! knob_err="$(deploy_validate_knobs "${HEALTH_TRIES-15}" "${HEALTH_INTERVAL-2}")"; then
    echo "ERROR: $knob_err" >&2
    exit 1
fi

BINARY="bin/containariumd-linux-amd64"
PROJECT="${PROJECT:-<your-gcp-project>}"
ZONE="${ZONE:-<your-zone>}"
PRIMARY_VM="${PRIMARY_VM:-<your-primary-vm>}"
SENTINEL_VM="${SENTINEL_VM:-<your-sentinel-vm>}"
# systemd unit names (override per host if the daemon runs under a
# different unit, e.g. PRIMARY_SERVICE=containarium-daemon).
PRIMARY_SERVICE="${PRIMARY_SERVICE:-containarium}"
SENTINEL_SERVICE="${SENTINEL_SERVICE:-containarium-sentinel}"
# After each restart the new binary must prove healthy or the previous one
# (kept as /usr/local/bin/containariumd.old) is put back and the deploy stops
# (#3060). The primary is checked with GET <PRIMARY_HEALTH_URL> on the host;
# the sentinel has no such endpoint, so its check is the unit staying active
# for the whole window (#3108).
# The window is HEALTH_TRIES x HEALTH_INTERVAL (defaults 15 x 2s = 30s). For
# the primary URL check, a healthy daemon that needs longer than that to start
# is rolled back like a broken one, so raise HEALTH_TRIES for slow hosts. The
# sentinel must be active on its first probe; HEALTH_TRIES only sets how long
# an active sentinel is watched. Both are plain integers
# without leading zeros: HEALTH_TRIES 1-9999, HEALTH_INTERVAL 0-9999 (a bare 0
# is valid); anything else is rejected before the first remote call.
# See scripts/lib/deploy-install.sh.
# Service names and health URLs end up inside single quotes in the remote
# command: they come from operator env and must not contain a single quote.
PRIMARY_HEALTH_URL="${PRIMARY_HEALTH_URL:-http://localhost:8080/health}"
SENTINEL_HEALTH_URL="${SENTINEL_HEALTH_URL:-}"
# Space-separated peer hostnames; defaults are placeholders.
PEERS=(${PEERS:-<peer-a> <peer-b>})

# Expected checksum of the binary we are shipping. Every transfer below is
# verified against this BEFORE the receiving host's service is stopped.
#
# This is not belt-and-braces: scp has been observed exiting 0 having written
# a truncated file — twice to the same peer, delivering 31MB then 65MB of a
# 143MB binary, with no error and plenty of free disk. Installing that and
# restarting leaves a host with a corrupt binary and a stopped daemon, which
# on a peer carrying tenant containers is an outage rather than a retry.
#
# Verifying before the stop means a bad transfer costs a retry instead.
verify_remote() {
    # verify_remote <label> <sha-command-runner...>
    # The runner is invoked with the remote shell command as its last argument.
    local label="$1"; shift
    local got
