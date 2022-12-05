#!/usr/bin/env bash
# Run from an extracted, signature-verified release: sudo bash install.sh.
# Installing a previous release again rolls current back atomically.
set -euo pipefail
source_dir=$(cd -- "$(dirname -- "$0")" && pwd)
root=${1:-/opt/containarium/toolchains}
release=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["release"])' "$source_dir/manifest.json")
arch=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["arch"])' "$source_dir/manifest.json")
[[ "$release" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,100}$ ]] || { echo 'invalid release id' >&2; exit 1; }
case "$(uname -m):$arch" in
    x86_64:amd64|aarch64:arm64) ;;
    *) echo "toolchain architecture mismatch: $arch" >&2; exit 1 ;;
esac
(cd "$source_dir" && sha256sum --quiet -c checksums.sha256)
mkdir -p "$root/releases"
destination="$root/releases/$release"
staged=""
link="$root/.current.$$"
cleanup() {
