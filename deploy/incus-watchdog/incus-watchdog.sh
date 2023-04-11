#!/usr/bin/env bash
#
# incus-watchdog — detect a wedged incus API and force-recover it (#755).
#
# incusd can hang with its process alive but its unix-socket API unresponsive:
# a cgo call into liblxc's command protocol blocks forever in recvmsg() on an
# unresponsive container monitor (the command socket has no receive timeout),
# and the goroutine holds a lock the whole time, so every API request wedges
# behind it. It recurs under container-churn workloads (create/delete storms).
# See the upstream fix proposal (lxc/lxc#4708) for the root cause.
#
# incusd's own SIGTERM/SIGQUIT handlers run on goroutines that are part of the
# same deadlock, so `systemctl restart` hangs in "deactivating" until
# TimeoutStopSec (incus ships a long one). SIGKILL to the MAIN process is
# unblockable and — because instances run in their own lxc.payload.* scopes,
# not as children of incusd — they SURVIVE the restart.
#
# This watchdog probes the API on an interval and, after N consecutive probe
# timeouts, SIGKILLs the main incusd and starts it again. Two consecutive
# failures (not one) are required so a single slow probe doesn't trigger a
# needless restart.
#
# Config via env (systemd EnvironmentFile or the unit's Environment=):
#   INCUS_WATCHDOG_INTERVAL         seconds between probes           (default 45)
#   INCUS_WATCHDOG_PROBE_TIMEOUT    seconds before a probe is failed (default 25)
#   INCUS_WATCHDOG_FAIL_THRESHOLD   consecutive fails before restart (default 2)
#   INCUS_WATCHDOG_UNIT             systemd unit to recover          (default incus.service)
#   INCUS_WATCHDOG_SETTLE           seconds to wait after a restart  (default 15)
set -uo pipefail

INTERVAL="${INCUS_WATCHDOG_INTERVAL:-45}"
PROBE_TIMEOUT="${INCUS_WATCHDOG_PROBE_TIMEOUT:-25}"
FAIL_THRESHOLD="${INCUS_WATCHDOG_FAIL_THRESHOLD:-2}"
UNIT="${INCUS_WATCHDOG_UNIT:-incus.service}"
SETTLE="${INCUS_WATCHDOG_SETTLE:-15}"

