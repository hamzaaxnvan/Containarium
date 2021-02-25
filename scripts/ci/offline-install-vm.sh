#!/usr/bin/env bash
# Published release verification on a fresh Ubuntu 24.04 VM with no NIC.
# Host needs QEMU, genisoimage, curl, git and sha256sum. No root required
# when /dev/kvm is readable/writable; otherwise use sudo or QEMU TCG.
# Evidence is retained in OUTPUT_DIR; only its scratch disks are ephemeral.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
tag=${RELEASE_TAG:-v0.121.0}
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Invalid RELEASE_TAG' >&2; exit 1; }
out=${OUTPUT_DIR:?Set OUTPUT_DIR to an empty evidence directory}
mkdir -p "$out"
out=$(cd "$out" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fetch() { curl -fsSL --retry 3 --connect-timeout 30 --max-time 300 "$1" -o "$2"; }

git -C "$here" fetch --depth=1 --no-tags https://github.com/FootprintAI/Containarium "refs/tags/$tag"
commit=$(git -C "$here" rev-parse 'FETCH_HEAD^{commit}')
file="containarium-bundle-${tag}-linux-amd64.tar.gz"
release="https://github.com/FootprintAI/Containarium/releases/download/$tag"
mkdir "$work/payload" "$work/seed"
echo "Downloading published bundle: $release/$file"
fetch "$release/$file" "$work/payload/$file"
fetch "$release/$file.sha256" "$out/bundle.sha256"
(cd "$work/payload" && sha256sum -c "$out/bundle.sha256")
bundle_sha=$(sha256sum "$work/payload/$file" | cut -d ' ' -f1)

