// Package toolaccess is the one mapping between the BoxToolAccess proto enum
// and the string stored in the user.containarium.tool_access Incus config key
// (#2944). The daemon (writing and reading the key) and the client (showing
// it) both use it, so the two cannot drift.
package toolaccess

import (
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// RunUser is the unprivileged OS user platform-started runs execute as on a box
// with a recorded tool-access level (#2944, slice 1b). One name shared by the
// daemon's run path and the gateway's terminal route (#3275), so the sign-in
// terminal can never open as a different user than the one the run is refused on.
