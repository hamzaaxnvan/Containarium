#!/usr/bin/env bash
# Table test for the box login hook (#2121):
# pkg/core/container/tmux_login_hook.sh, installed as
# /etc/profile.d/containarium-tmux.sh in every box.
#
# Runs the REAL hook under a real shell on a real pty (python3's pty module),
# with a fake `tmux` on PATH that records its argv. Each row is one login
# shape; the assertion is whether the stub was asked for `new -A -s main`,
# and whether the shell fell through to a normal prompt afterwards.
#
# No image build and no sshd: the "ssh shapes" are reproduced by the three
# things sshd actually varies (login argv0 "-bash" or not, pty or not,
# SSH_TTY set or not). What this cannot prove is that a given sshd / IDE /
# mosh really produces those shapes; see the PR for that list.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOOK="$ROOT/pkg/core/container/tmux_login_hook.sh"
BASH_BIN="$(command -v bash)"
PY_BIN="$(command -v python3)"

for dep in python3 bash; do
  command -v "$dep" >/dev/null 2>&1 || { echo "SKIP: $dep not available"; exit 0; }
done

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# --- fake tmux ------------------------------------------------------------
# Records argv (one invocation per line) to $TMUX_STUB_LOG. `-V` succeeds
# unless TMUX_STUB_V_RC says otherwise; `new ...` exits TMUX_STUB_NEW_RC.
stub="$tmp/stubbin"
mkdir -p "$stub"
cat > "$stub/tmux" <<'STUB'
#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_STUB_LOG"
case "$1" in
  -V) echo "tmux 3.4-stub"; exit "${TMUX_STUB_V_RC:-0}" ;;
esac
exit "${TMUX_STUB_NEW_RC:-0}"
STUB
chmod +x "$stub/tmux"

# A PATH with the stub but WITHOUT any real tmux, and one with no tmux at all.
nobin="$tmp/nobin"
mkdir -p "$nobin"
for b in cat sh bash dash env mkdir; do
  p="$(command -v "$b" 2>/dev/null)" && ln -s "$p" "$nobin/$b"
done
PATH_STUB="$stub:$nobin"
PATH_NOTMUX="$nobin"

# mosh-server stand-in: a script NAMED mosh-server (so /proc/<pid>/comm of
# the parent reads "mosh-server", as for the real daemon) that starts the
# login shell as a CHILD, the way mosh-server does.
# The shebang must name bash directly: via /usr/bin/env the comm would be
# re-set to "bash" by env's own exec.
# shellcheck disable=SC2016 # expanded by the stub at run time, not here
{ echo "#!$BASH_BIN"; echo '( exec -a -bash "$REAL_BASH" --noprofile --norc -i )'; } > "$stub/mosh-server"
