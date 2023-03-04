//go:build !windows

package cloud

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/footprintai/containarium/internal/safecast"
)

// diskDataDir is where the daemon's Incus storage lives — the disk the host's
// container workloads consume. statfs here reflects the capacity that
// actually matters for scheduling. Falls back to "/" when absent.
const diskDataDir = "/var/lib/incus"

// diskGB returns (total, available) disk in GB for the daemon's data dir via
// statfs. Best-effort: (0, 0) when neither the data dir nor "/" can be
// stat-ed. Computed in uint64 so it's correct whether the platform's Bsize is
// signed (Linux int64) or unsigned (darwin uint32).
func diskGB() (total, avail int32) {
	path := diskDataDir
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		if err := syscall.Statfs("/", &st); err != nil {
			return 0, 0
		}
	}
	bsize := safecast.U64FromI64(int64(st.Bsize))
	const bytesPerGB = 1024 * 1024 * 1024
	total = safecast.I32(safecast.I64FromU64(uint64(st.Blocks) * bsize / bytesPerGB))
