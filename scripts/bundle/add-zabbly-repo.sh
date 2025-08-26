#!/usr/bin/env bash
# add-zabbly-repo.sh — configure the Zabbly Incus stable apt repo on an
# Ubuntu bundle builder, so download-deps.sh can bundle incus, incus-client
# and incus-extra: the packages a connected install gets (hacks/install.sh).
# Ubuntu has no incus-extra, and a stock runner has no Zabbly repo (#3061).
#
# The signing key is pinned by fingerprint: a downloaded key whose only
# primary key is not ZABBLY_KEY_FPR is refused before anything is written.
# Bump the pin only after checking the new fingerprint out of band.
#
# Usage: scripts/bundle/add-zabbly-repo.sh   (uses sudo when not root)
set -euo pipefail

# Zabbly Kernel Builds <info@zabbly.com>, rsa3072, expires 2030-08-17.
