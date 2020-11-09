package container

import (
	"fmt"
	"log"

	"github.com/footprintai/containarium/pkg/core/ostype"
)

// cgroupWrapperScript returns a bash wrapper script that intercepts run/create
// subcommands and injects --memory/--cpus flags from the LXC container's live
// cgroup v2 limits. This ensures nested Docker/Podman containers see the
// correct resource limits instead of the physical host's resources.
//
// For Docker, the OCI runtime wrapper (see ociRuntimeScript) catches all
// container creation including Docker Compose v2. The CLI wrapper is kept as
// a compatible belt-and-suspenders layer.
func cgroupWrapperScript(realBinary string) string {
	return fmt.Sprintf(`#!/bin/bash
# Containarium: cgroup-limit-aware wrapper for nested containers.
# Injects --memory and --cpus from LXC cgroup v2 limits so that
# nested containers see the correct resource constraints.
# NOTE: Docker Compose v2 uses the Docker API directly and bypasses this wrapper.
REAL=%s

case "${1:-}" in
  run|create)
    # Skip injection if user already set the flag
    for a in "$@"; do
      case "$a" in --memory=*|--memory|-m=*|-m) HAS_MEM=1;; esac
      case "$a" in --cpus=*|--cpus) HAS_CPU=1;; esac
    done

    INJECT=""

    # Memory: read /sys/fs/cgroup/memory.max (bytes, or "max")
    if [ -z "${HAS_MEM:-}" ] && [ -r /sys/fs/cgroup/memory.max ]; then
      MEM=$(cat /sys/fs/cgroup/memory.max)
      [ "$MEM" != "max" ] && [ "$MEM" -gt 0 ] 2>/dev/null && INJECT="--memory=$MEM"
    fi

    # CPU: read /sys/fs/cgroup/cpu.max ("$QUOTA $PERIOD" or "max $PERIOD")
    if [ -z "${HAS_CPU:-}" ] && [ -r /sys/fs/cgroup/cpu.max ]; then
      read -r QUOTA PERIOD < /sys/fs/cgroup/cpu.max
      if [ "$QUOTA" != "max" ] && [ "$PERIOD" -gt 0 ] 2>/dev/null; then
        CPUS=$(awk "BEGIN {printf \"%%.2f\", $QUOTA/$PERIOD}")
        INJECT="$INJECT --cpus=$CPUS"
      fi
    fi

    if [ -n "$INJECT" ]; then
      SUB="$1"; shift
      exec $REAL "$SUB" $INJECT "$@"
    fi
    ;;
esac
exec $REAL "$@"
`, realBinary)
}

// ociRuntimeScript returns a bash script that acts as a custom OCI runtime.
// It wraps the real runc binary and injects cgroup resource limits from the
// LXC container's cgroup v2 into the OCI spec's config.json on "create".
// This catches ALL container creation paths including Docker Compose v2,
// which bypasses the CLI wrapper by using the Docker Engine API directly.
//
// The script is installed at /usr/local/bin/containarium-runtime and registered
