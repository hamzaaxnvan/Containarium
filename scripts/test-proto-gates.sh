#!/usr/bin/env bash
# Exercise the same Makefile gates CI uses, including their failure paths.
set -euo pipefail
repo="$(cd "$(dirname "$0")/.." && pwd)"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

make -C "$repo" proto-lint
echo 'PASS: repository protos pass lint'

# A mismatched tool that would otherwise accept every command must be refused.
source "$repo/scripts/buf-version.env"
cat > "$scratch/old-buf" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == --version ]]; then echo 1.38.0; fi
exit 0
EOF
chmod +x "$scratch/old-buf"
if BUF="$scratch/old-buf" make -C "$repo" proto-lint > "$scratch/version.log" 2>&1; then
  echo 'FAIL: accepted the wrong buf version' >&2
  exit 1
