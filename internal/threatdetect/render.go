package threatdetect

import (
	"math"

	"github.com/footprintai/containarium/internal/netbpf"
)

// safeInt64 clamps a uint64 traffic counter (flow bytes/packets, deny
// counts) to int64 range rather than letting a value above 2^63 wrap to a
// negative evidence field (same guarded-clamp pattern as
// internal/sentinel/metricsexport.go's safeInt64 — duplicated locally, not
// exported, for the same reason protoName is above). Real flow counters are
// nowhere near this; the clamp exists so a wrap would be impossible rather
// than merely unlikely.
func safeInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}
