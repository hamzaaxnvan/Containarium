#!/usr/bin/env bash
# Restore a tenant's backup — the newest one by default (#1839: retention
# now keeps several, not just one, so "the current good dump" means "the
# newest", not "the only one").
#
# Usage:
#   restore-tenant.sh <tenant> [--database <name>] [--id <backup-id>] [--dry-run]
#
# --id targets a specific backup instead of the newest — use it to restore
# an older point in time. `containarium backup list <tenant>` shows what's
# available (newest first) with each id's timestamp.
#
# Required environment variables (same as backup-all-tenants.sh, loaded from
# /etc/containarium/backup.env by the operator or the calling shell):
#   CONTAINARIUM_SERVER  daemon address, e.g. localhost:8080
#
# Optional:
#   CONTAINARIUM_AUTH_TOKEN  JWT for --token
#   CONTAINARIUM_BIN         path to containarium binary

set -uo pipefail

usage() {
  echo "Usage: $0 <tenant> [--database <name>] [--id <backup-id>] [--dry-run]" >&2
  exit 1
}

[[ $# -eq 0 ]] && usage

TENANT="$1"; shift
SERVER="${CONTAINARIUM_SERVER:?CONTAINARIUM_SERVER must be set}"
