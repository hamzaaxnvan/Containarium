#!/usr/bin/env bash
# Stale-install-path gate for the containarium -> containariumd rename
# (#1781, docs/architecture/cli-client-server-split.md Test strategy).
#
# Every host-side script/unit/terraform file must install, download, or
# reconcile the daemon binary as /usr/local/bin/containariumd — the old
# path is legitimate ONLY as the compat symlink target (`ln -sf
# .../containariumd /usr/local/bin/containarium`), as the symlink-removal
# check in hacks/uninstall.sh, or in tests that deliberately exercise the
# mid-rollout / pre-rollout state. A bare, non-allow-listed hit means some
# script still downloads or installs the OLD path as if it were the real
# binary — exactly the class of bug #1781 exists to catch.
#
# Portability note: this deliberately avoids grep -P (a negative-lookahead
# version of this gate silently misbehaved on stock macOS, whose BSD grep
# has no -P at all — flagged on #1797). Instead of "containarium not
# followed by a word/hyphen char", we capture the FULL identifier run
# after the path (grep -oE '...containarium[a-zA-Z0-9_-]*') and check in
# the shell whether that run is exactly "containarium" — everything else
# (containariumd, containarium-shell, containarium-info,
# containarium-runner-loop, containarium-zfs-backup, ...) is a different,
# unrelated binary and never matches. -n/-o/-E are POSIX and behave
# identically under GNU and BSD grep.
set -uo pipefail
cd "$(dirname "$0")/.."

PATTERN='/usr/local/bin/containarium[a-zA-Z0-9_-]*'
OLD_PATH='/usr/local/bin/containarium'
