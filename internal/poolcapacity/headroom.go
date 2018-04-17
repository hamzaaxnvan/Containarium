package poolcapacity

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
)

// Headroom floor (#3128). A pool that fills to 0 B stops Incus, core Postgres
// and then the daemon itself (#3043). Past the floor, the daemon refuses the
// work that grows a pool the fastest — container creates and snapshots — with
// an error naming the pool and the headroom left, instead of filling it.
//
// The floor is percent used against the Incus-reported total, the same unit
// as WarnPercent and CritPercent. It is configured per pool with the daemon's
// repeatable --pool-headroom-floor=<pool>=<percent> flag (decision Q6 on
// #3128). It moves only the refusal: host status and the built-in alert keep
// the fixed WarnPercent/CritPercent.

// DefaultFloorPercent is the floor for a pool not named on the flag.
const DefaultFloorPercent = 95

// A configured floor must lie in [minFloorPercent, maxFloorPercent] and above
// WarnPercent, so "pool nearly full" in host status always shows before work
// starts being refused.
const (
	minFloorPercent = 50
	maxFloorPercent = 99
)

// Floors maps a pool name to its floor in percent used. It implements
// pflag.Value, so the daemon flag is repeatable and validated at parse time.
// A nil Floors is valid: every pool gets DefaultFloorPercent.
type Floors map[string]int

// Set parses one "<pool>=<percent>" spec.
func (f Floors) Set(spec string) error {
	name, pctStr, ok := strings.Cut(spec, "=")
	if !ok {
		return fmt.Errorf("pool headroom floor %q: want <pool>=<percent>", spec)
	}
