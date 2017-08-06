#!/usr/bin/env bash
# Remote install step shared by scripts/deploy-binary.sh and its test (#3060).
#
# deploy_install_cmd <service> [health-url] prints one shell command for the
# target host. It stops the unit, keeps the binary it is replacing as
# <bin>.old, installs the new one, starts the unit and checks health. If the
# unit does not become healthy it puts <bin>.old back, starts the unit again
# and exits non-zero, so the caller's ssh (and `set -e`) stops the rollout.
#
# Health is `curl -f <health-url>` when a URL is given: the first success
# within the window passes. Without a URL (the sentinel) health is the unit
# staying active, in the same systemd invocation (InvocationID unchanged), for
# the whole window, so a binary that is active right after
# `systemctl start` and crashes a moment later still rolls back (#3108).
#
# Knobs, read when the command is built: BIN_DIR (default /usr/local/bin),
# SRC (default /tmp/containariumd), HEALTH_TRIES (default 15), HEALTH_INTERVAL
# seconds (default 2). The window is HEALTH_TRIES x HEALTH_INTERVAL, 30s by
# default. Both must be plain decimal integers, no sign and no leading zeros
# (a bare 0 is valid for HEALTH_INTERVAL): HEALTH_TRIES 1 to 9999,
# HEALTH_INTERVAL 0 to 9999. Anything else, including an empty value, is
# rejected; scripts/deploy-binary.sh checks this before its first remote call,
# and deploy_install_cmd checks again before it builds the command.
# A successful sentinel deploy always takes the full window. A daemon that is
# healthy but needs longer than the window to come up (URL path) or to settle
# is rolled back like a broken one: raise HEALTH_TRIES
# (or HEALTH_INTERVAL) for a slow-starting daemon, e.g.
#   HEALTH_TRIES=60 HEALTH_INTERVAL=2 scripts/deploy-binary.sh ...
#
# deploy_validate_knobs <tries> <interval> prints the problem(s) on one line
# and returns 1 when a knob is unusable; prints nothing and returns 0 otherwise.
# HEALTH_TRIES is 1..9999, HEALTH_INTERVAL is 0..9999 seconds: plain decimal
# integers, no sign, no leading zeros (a bare 0 is valid for the interval only),
# at most 4 digits. Values are quoted with %q so a control character in the
# value never reaches the terminal. The check runs under LC_ALL=C, so digits of
# other scripts never match. scripts/deploy-binary.sh calls this before
# its first remote call; deploy_install_cmd calls it again.
deploy_validate_knobs() {
    local LC_ALL=C tries="$1" interval="$2" err="" q
    case "$tries" in
        [1-9]|[1-9][0-9]|[1-9][0-9][0-9]|[1-9][0-9][0-9][0-9]) ;;
