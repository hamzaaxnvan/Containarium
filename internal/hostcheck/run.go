//go:build !windows

package hostcheck

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// RequiredCaps are the effective capabilities the daemon needs to manage
// per-tenant Linux users (useradd, chown ~, etc.). Bit numbers per
// <linux/capability.h>.
var RequiredCaps = []struct {
	Name string
	Bit  uint
}{
	{"CAP_CHOWN", 0},
	{"CAP_DAC_OVERRIDE", 1},
	{"CAP_FOWNER", 3},
	{"CAP_SETGID", 6},
	{"CAP_SETUID", 7},
}

// DaemonWritablePaths must be writable for the daemon (the unit's
// ReadWritePaths) plus /var/log — useradd touches /var/log/lastlog, the
// "second, independent trap" the deploy contract calls out.
// /var/lib/containarium holds the backup sidecar index and staged dumps
// (pkg/core/backup writes there for every destination, not just LOCAL), so the
// daemon needs it writable or every backup fails EROFS under
// ProtectSystem=strict.
var DaemonWritablePaths = []string{
	"/var/lib/incus", "/etc/containarium", "/etc", "/home",
	"/var/lock", "/run/lock", "/opt/containarium", "/var/lib/containarium",
	"/var/log",
}

// Run executes every host capability check and returns the results. Pure of
// CLI concerns so the `doctor` / `pool join` CLIs, the daemon startup
// self-check, and the cloud status probe all share one implementation.
func Run() []Check {
	var checks []Check

	// 1. Running as (effective) root.
	euid := os.Geteuid()
	checks = append(checks, Check{
		Name: "running as root", OK: euid == 0, Required: true,
		Detail: fmt.Sprintf("euid=%d (need 0)", euid),
	})

	// 2. Effective capabilities. Diagnostic — the useradd probe (4) is the
	// definitive test; a host can lack the readable mask yet still work, or
	// have it yet be broken by other sandboxing.
