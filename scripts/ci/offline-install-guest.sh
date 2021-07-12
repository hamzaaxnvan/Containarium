#!/usr/bin/env bash
# Run only in the disposable Ubuntu VM created by offline-install-vm.sh.
set -euo pipefail

check_install() {
  local log=$1 audit
  grep -F 'Containarium Air-Gapped Installation Complete' "$log" >/dev/null || return 1
  if grep -E '(^E:|apt-get install exited non-zero|^dpkg: error|^Errors were encountered while processing:)' "$log"; then
    echo 'FAIL: installer concealed an apt/dpkg failure' >&2
    return 1
  fi
  for cmd in incus zfs jq curl; do
    command -v "$cmd" || return 1
  done
  audit=$(dpkg --audit)
  if [ -n "$audit" ]; then
    printf 'FAIL: dpkg audit: %s\n' "$audit" >&2
    return 1
  fi
}

if [ "${1:-}" = --check-install ]; then
  check_install "$2"
  exit
fi

tag=$1
commit=$2
bundle_sha=$3
[ "$EUID" -eq 0 ]
# shellcheck source=/dev/null
. /etc/os-release
[ "$ID" = ubuntu ] && [ "$VERSION_ID" = 24.04 ]
printf 'Release: %s\nCommit: %s\nBundle SHA256: %s\nOS: %s\n' "$tag" "$commit" "$bundle_sha" "$PRETTY_NAME"
echo 'Network devices before installation:'
ls /sys/class/net
for dev in /sys/class/net/*; do
  [ "${dev##*/}" = lo ] || { echo 'FAIL: guest has a network interface'; exit 1; }
done
ip route show
ip -6 route show
for cmd in incus zfs jq; do
  if command -v "$cmd"; then echo "FAIL: $cmd was preinstalled"; exit 1; fi
done
echo 'PASS: pristine Ubuntu 24.04; no Incus, ZFS, jq or network adapter'

mkdir /opt/offline-verification
cd /opt/offline-verification
file="containarium-bundle-${tag}-linux-amd64.tar.gz"
printf '%s  /payload/%s\n' "$bundle_sha" "$file" | sha256sum -c -
tar xzf "/payload/$file"
bundle="/opt/offline-verification/containarium-bundle-${tag}-linux-amd64"
[ "$(cat "$bundle/VERSION")" = "${tag#v}" ]
[ ! -d "$bundle/images" ]
for pkg in incus incus-extra zfsutils-linux jq curl; do
