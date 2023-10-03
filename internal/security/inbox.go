package security

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// The in-box ClamAV scan runner (#2631, #2658).
//
// The scan executes INSIDE the tenant's own box, as an unprivileged user with
// only the capability to read files, so the platform never mounts or streams a
// tenant's files and a scanner bug parsing malware never runs as box root (and,
// in a privileged box, never as host root). What leaves the box is a verdict
// record: status, signature names with counts, duration, scanner version. File
// paths are not kept (a path under a home directory is customer data).

// MinBoxMemoryBytes is the memory a box needs to run ClamAV: a one-shot
// clamscan loads about 1.5 GB of signatures. It equals the default box size
// ("4GB", decimal), so a default box is supported. Incus reads GB as 10^9 and
// GiB as 2^30, so both "4GB" and "4GiB" pass.
const MinBoxMemoryBytes int64 = 4_000_000_000

// DefaultInBoxScanTimeout bounds one scan.
const DefaultInBoxScanTimeout = 30 * time.Minute

// Why a scan produced no verdict. Stored in the report's findings text for
// every status other than clean/infected.
const (
	reasonNotRunning    = "box is not running"
	reasonNoClamscan    = "clamscan is not installed in the box (run: containarium security clamav install)"
	reasonNoSignatures  = "no ClamAV signatures in the box (run freshclam, or: containarium security clamav install)"
	reasonScanError     = "scan error (clamscan exit 2)"
	reasonOOM           = "scan was killed, most likely out of memory (box limit)"
	reasonTimeout       = "scan timed out"
	reasonUnexpectedRC  = "unexpected clamscan exit code"
	reasonTooSmallFmt   = "box memory limit %s is below the %d GB ClamAV needs"
	reasonExecFailedFmt = "could not run the scan in the box: %v"
)

// boxAPI is the slice of the Incus client the runner needs.
// *incus.Client satisfies it.
type boxAPI interface {
	GetContainer(name string) (*incus.ContainerInfo, error)
	ExecWithExitCode(containerName string, command []string) (stdout, stderr string, exitCode int, err error)
}

// parseMemoryBytes parses an Incus limits.memory value. ok is false for an
// empty value, a percentage, or anything it does not understand — the caller
// then does not block the scan on size.
func parseMemoryBytes(v string) (bytes int64, ok bool) {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasSuffix(v, "%") {
		return 0, false
	}
	units := []struct {
		suffix string
