#!/usr/bin/env bash
# Live #1139 verification. Stops ONE configured backend VM; run only against
# an isolated pair or the sprint's authorized batched verification environment.
# See docs/testing/sentinel-outage-recovery.md for inputs and evidence.
set -euo pipefail

fail() { echo "FAIL: $*" >&2; exit 1; }
for name in GCP_PROJECT GCP_ZONE BACKEND_VM SENTINEL_SSH_TARGET BACKEND_SSH_TARGET APP_SSH_TARGET SENTINEL_HTTPS_IP APP_URL EXPECTED_HTTPS_BODY; do
  [[ -n "${!name:-}" ]] || fail "set $name (see docs/testing/sentinel-outage-recovery.md)"
done
OUTAGE_SECONDS="${OUTAGE_SECONDS:-185}"
KEYSYNC_INTERVAL_SECONDS="${KEYSYNC_INTERVAL_SECONDS:-120}"
RECOVERY_SECONDS="${RECOVERY_SECONDS:-30}"
BOOT_TIMEOUT_SECONDS="${BOOT_TIMEOUT_SECONDS:-300}"
BACKEND_HEALTH_PORT="${BACKEND_HEALTH_PORT:-8080}"
SENTINEL_UNIT="${SENTINEL_UNIT:-containarium-sentinel.service}"
for duration in "$OUTAGE_SECONDS" "$KEYSYNC_INTERVAL_SECONDS" "$RECOVERY_SECONDS" "$BOOT_TIMEOUT_SECONDS"; do
  [[ "$duration" =~ ^[1-9][0-9]{0,6}$ ]] || fail "durations must be positive integer seconds"
done
(( OUTAGE_SECONDS > KEYSYNC_INTERVAL_SECONDS )) || fail "outage must exceed the configured keysync interval"
[[ "$BACKEND_HEALTH_PORT" =~ ^[1-9][0-9]{0,4}$ ]] && (( BACKEND_HEALTH_PORT <= 65535 )) || fail "invalid backend health port"
[[ "$SENTINEL_UNIT" =~ ^[a-zA-Z0-9_@.][a-zA-Z0-9_@.-]*$ ]] || fail "invalid sentinel unit"
[[ "$APP_URL" == https://* ]] || fail "APP_URL must use HTTPS"
for tool in gcloud ssh curl timeout; do command -v "$tool" >/dev/null || fail "missing $tool"; done

ssh_options=(-o BatchMode=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=5 -o ConnectionAttempts=1 -o ControlMaster=no -o ControlPath=none)
backend_ip() {
  gcloud compute instances describe "$BACKEND_VM" --project "$GCP_PROJECT" --zone "$GCP_ZONE" \
    --format 'value(networkInterfaces[0].networkIP)'
}
start_backend() {
  gcloud compute instances start "$BACKEND_VM" --project "$GCP_PROJECT" --zone "$GCP_ZONE" --quiet
}
identity() {
  timeout 8s ssh "${ssh_options[@]}" "$SENTINEL_SSH_TARGET" \
    "systemctl show '$SENTINEL_UNIT' --property=MainPID --property=NRestarts --property=ExecMainStartTimestampMonotonic"
}
probe_ssh() {
  local body
  body="$(timeout 8s ssh "${ssh_options[@]}" "$APP_SSH_TARGET" 'printf sentinel-outage-ssh-ok' 2>/dev/null)" &&
    [[ "$body" == sentinel-outage-ssh-ok ]]
}
probe_https() {
  local body
  body="$(curl --silent --show-error --fail --max-time 8 --noproxy '*' \
    --connect-to "::${SENTINEL_HTTPS_IP}:" "$APP_URL" 2>/dev/null)" && [[ "$body" == "$EXPECTED_HTTPS_BODY" ]]
}
backend_ready() {
  timeout 8s ssh "${ssh_options[@]}" "$BACKEND_SSH_TARGET" \
    "curl -fsS --max-time 5 http://localhost:$BACKEND_HEALTH_PORT/health >/dev/null" 2>/dev/null
}
needs_start=false
cleanup() {
  local rc=$?
  trap - EXIT
  if "$needs_start"; then
    echo "Restoring backend after failure" >&2
    start_backend || rc=1
  fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

baseline="$(identity)" || fail "cannot read sentinel identity"
grep -Eq '^MainPID=[1-9][0-9]*$' <<< "$baseline" || fail "sentinel service is not running"
initial_ip="$(backend_ip)"
[[ -n "$initial_ip" ]] || fail "backend has no internal IP"
probe_ssh || fail "SSH baseline failed"
probe_https || fail "HTTPS baseline failed"
backend_ready || fail "backend health baseline failed"
assert_sentinel_alive() {
  local current
  current="$(identity)" || fail "sentinel became unreachable"
  [[ "$current" == "$baseline" ]] || fail "sentinel restarted"
}

echo "Starting outage: $(date -u +%FT%TZ); requested duration=${OUTAGE_SECONDS}s"
# Set before stopping: even an ambiguous stop failure must attempt recovery.
needs_start=true
gcloud compute instances stop "$BACKEND_VM" --project "$GCP_PROJECT" --zone "$GCP_ZONE" --quiet
outage_start=$SECONDS
if probe_ssh; then fail "SSH still works with the backend stopped; check the test target"; fi
if probe_https; then fail "HTTPS still works with the backend stopped; check the test target"; fi
