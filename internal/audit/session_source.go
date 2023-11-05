package audit

import (
	"context"
	"strings"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// Backend-neutral session collection (#1189).
//
// The collector's own job is scheduling, de-duplication and writing audit
// entries — none of which is backend-specific. What IS backend-specific is
// three things that travel together, and the interface groups them for that
// reason rather than for tidiness:
//
//   - which boxes to read
//   - how to read a box's session log
//   - how to parse a line of it
