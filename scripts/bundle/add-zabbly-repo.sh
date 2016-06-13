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
ZABBLY_KEY_FPR=4EFC590696CB15B87C73A3AD82CC8797C838DCFD
KEYRING=/usr/share/keyrings/zabbly-incus.gpg
SOURCES=/etc/apt/sources.list.d/zabbly-incus-stable.list

SUDO=""
[ "$(id -u)" -eq 0 ] || SUDO=sudo

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -m 700 "$work/gnupg"

curl -fsSL --retry 3 -o "$work/key.asc" https://pkgs.zabbly.com/key.asc

fprs="$(GNUPGHOME="$work/gnupg" gpg --batch --show-keys --with-colons "$work/key.asc" \
