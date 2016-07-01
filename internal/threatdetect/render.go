package threatdetect

import (
	"math"

	"github.com/footprintai/containarium/internal/netbpf"
)

// safeInt64 clamps a uint64 traffic counter (flow bytes/packets, deny
// counts) to int64 range rather than letting a value above 2^63 wrap to a
