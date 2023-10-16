package cmd

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// renderStoragePools is the "Storage Pools" block of `containarium info`
// (#2823): each pool's fill against its size, flagged "WARNING: pool nearly
// full" (>= 85%, #3128) or CRITICAL (>= 95%). Empty when the daemon reported no pools (local mode, an
// older daemon, or a monitor that has not read yet).
