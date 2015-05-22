//go:build !windows

package hostcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultCgroupSystemSlice is where cgroup v2 exposes each system unit's
// realized resource controls on an Ubuntu 24.04 host.
const DefaultCgroupSystemSlice = "/sys/fs/cgroup/system.slice"

// platformCPUWeightCheck reports whether incusd and the containarium daemon
