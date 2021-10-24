package server

import (
	"context"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The run journal reaper (#2096): member boxes are long-lived and reused
// across runs, so every run leaves a directory under runJournalRoot. A daemon
// sweep removes the ones older than the retention window.

// DefaultRunJournalRetention is how long a run's journal stays on a member box.
const DefaultRunJournalRetention = 7 * 24 * time.Hour

// runJournalSweepInterval is how often the reaper sweeps every agent box.
const runJournalSweepInterval = time.Hour

// clearRunJournalsScript empties the journal root; provisionSkillBox runs it
// on a freshly built box.
const clearRunJournalsScript = "rm -rf " + runJournalRoot

// runJournalListScript prints "<mtime-epoch> <name>" per run directory. A
// missing root prints nothing.
const runJournalListScript = `[ -d ` + runJournalRoot + ` ] || exit 0
for d in ` + runJournalRoot + `/*/; do
  [ -d "$d" ] || continue
  echo "$(stat -c %Y "$d") $(basename "$d")"
done`

// runDir is one run directory on a box.
type runDir struct {
	name  string
	mtime time.Time
}

// parseRunDirListing parses runJournalListScript output, skipping lines it
// cannot read.
func parseRunDirListing(out string) []runDir {
	var dirs []runDir
	for _, line := range strings.Split(out, "\n") {
		secs, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(secs, 10, 64)
		if err != nil {
			continue
		}
		dirs = append(dirs, runDir{name: name, mtime: time.Unix(n, 0)})
	}
	return dirs
}

// staleRunDirs returns the run directories last modified more than window
// before now, sorted. A name that is not a safe run id is never returned, so
// the removal can only ever name a single directory under the root.
func staleRunDirs(dirs []runDir, now time.Time, window time.Duration) []string {
	var out []string
	for _, d := range dirs {
		if validRunPathID(d.name) && now.Sub(d.mtime) > window {
			out = append(out, d.name)
		}
