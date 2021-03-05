#!/usr/bin/env bash
# #3258: real apt, synthetic debs and an empty dpkg state/cache. Only dpkg
# is replaced: the test must exercise apt's archive acquisition without
# installing anything on the host. APT_CONFIG disables host hooks/sources.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
installer=$here/../../hacks/offline-install/install.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/state/lists/partial" "$tmp/cache/archives/partial" "$tmp/bundle apt" \
  "$tmp/etc/apt.conf.d" "$tmp/etc/sources.list.d" "$tmp/log"
: >"$tmp/state/status"
: >"$tmp/etc/apt.conf"
: >"$tmp/etc/sources.list"
for cmd in incus zfs jq curl; do ln -s /bin/true "$tmp/bin/$cmd"; done
cat >"$tmp/bin/dpkg" <<'EOF'
