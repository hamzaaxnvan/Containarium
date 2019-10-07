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
// small enough to protect the cloud bill if an autoscaler (or an
// agent) keeps asking for more. Unlike MaxRunnerCount — which
// bounds a SINGLE provision call — this bounds the WHOLE fleet
// across every caller (CLI, MCP, the daemon reconciler).
const DefaultMaxRunnersTotal = 20

// MaxRunnersTotal resolves the system-wide runner ceiling. It
// reads MAX_RUNNERS_TOTAL from the environment and falls back to
// DefaultMaxRunnersTotal when the var is unset, unparseable, or
// non-positive. Kept as a function (not a package var) so a value
// change picks up without a process restart and so tests can flip
// it with t.Setenv.
func MaxRunnersTotal() int {
	raw := strings.TrimSpace(os.Getenv("MAX_RUNNERS_TOTAL"))
