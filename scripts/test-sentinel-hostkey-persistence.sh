#!/usr/bin/env bash
# Regression test for #1596: the sentinel's admin sshd (port 2222) host key
# must survive repeated runs of startup-sentinel.sh — it was observed to
# regenerate on every containarium-sentinel restart, degrading
# StrictHostKeyChecking from a real MITM signal into routine noise.
#
# Helper copied verbatim from terraform/modules/containarium/scripts/
# startup-sentinel.sh (the /etc/containarium path is real production
# state there; here it's redirected under a throwaway temp dir so this
# test never touches the real filesystem).
set -uo pipefail

generate_admin_hostkey() { # root — path to use in place of /etc/containarium
    local root="$1"
    mkdir -p "$root/etc/containarium"
    if [ ! -f "$root/etc/containarium/ssh_host_ed25519_key" ]; then
        ssh-keygen -t ed25519 -f "$root/etc/containarium/ssh_host_ed25519_key" -N "" -q
        echo "admin sshd host key generated"
    fi
    chmod 600 "$root/etc/containarium/ssh_host_ed25519_key"
}
# --- end helper ---

pass=0; fail=0
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

first_out="$(generate_admin_hostkey "$tmpdir")"
if [ ! -f "$tmpdir/etc/containarium/ssh_host_ed25519_key" ]; then
  echo "  FAIL  first run creates a key"; fail=$((fail+1))
else
  echo "  PASS  first run creates a key"; pass=$((pass+1))
fi
if [ "$first_out" = "admin sshd host key generated" ]; then
  echo "  PASS  first run reports generating a key"; pass=$((pass+1))
else
  echo "  FAIL  first run did not report generating a key (got: $first_out)"; fail=$((fail+1))
