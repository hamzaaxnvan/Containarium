package runner

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultMaxRunnersTotal is the system-wide ceiling on how many
// runner boxes may exist at once, used when MAX_RUNNERS_TOTAL is
// unset/invalid. 20 is comfortably above a typical CI fan-out,
// well under the per-call MaxRunnerCount=100 sanity bound, and
