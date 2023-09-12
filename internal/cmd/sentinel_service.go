//go:build !windows && !containarium_client

package cmd

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

const sentinelSystemdServicePath = "/etc/systemd/system/containarium-sentinel.service"

// procMeminfoPath is a variable so tests can point it at a fixture.
var procMeminfoPath = "/proc/meminfo"

// hostHasSwap reports whether this host can page anonymous memory out, which
// is what decides whether a MemoryHigh below MemoryMax is a safety valve or the
// #1454 livelock.
//
// Unreadable or unparseable /proc/meminfo is reported as NO swap. That is the
// conservative direction: it only ever tightens the check, and the failure it
// guards against (a cgroup stalled below its cap, never restarted, still
// reporting healthy) is far more expensive than an install that asks the
// operator to be explicit.
func hostHasSwap() bool {
	data, err := os.ReadFile(procMeminfoPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "SwapTotal:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return false
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return false
		}
		return kb > 0
	}
	return false
}

var (
	sentinelSvcSpotVM     string
	sentinelSvcZone       string
	sentinelSvcProject    string
	sentinelSvcMemoryHigh string
	sentinelSvcMemoryMax  string
)

var sentinelServiceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage the Containarium sentinel systemd service",
}

var sentinelServiceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the sentinel systemd service file and enable it",
	Long: `Install the Containarium sentinel systemd service to /etc/systemd/system/.

The sentinel service monitors a backend spot VM and forwards traffic via iptables DNAT.
When the backend is unavailable, it serves a maintenance page.

Requires root privileges.`,
