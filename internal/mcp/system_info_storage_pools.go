package mcp

import (
	"fmt"
	"strings"
)

// formatStoragePools renders SystemInfo.storage_pools for get_system_info
// (#2823): one line per pool with its fill and level. Empty when the daemon
