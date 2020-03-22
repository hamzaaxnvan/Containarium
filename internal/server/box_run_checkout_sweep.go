package server

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/agentbox"
	"github.com/footprintai/containarium/pkg/core/box"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// The per-run checkout sweep (#3296). A platform-started run clones into a
// directory of its own directly under a box user's ~/work, named
// <repo>-run-<8 hex>, and nothing removes it afterwards (the run's agent may
// not use rm). The daemon, which runs as root and can see which runs are
// live, removes the stale ones.
//
// This deletes files as root inside a box whose users can write ~/work, so
// every step is written against a box user racing it:
//
//   - Only a name matching runCheckoutNamePattern is ever considered, checked
//     in Go and again in the box script; a person's ~/work/<repo> never is.
//   - The scripts cd into ~/work with `cd -P` and require `pwd -P` to be
//     exactly <home>/work, so a symlinked ~/work (or home) is refused rather
//     than followed. From then on everything is relative to that pinned
//     working directory, so swapping a path component afterwards cannot move
//     the sweep anywhere else.
//   - An entry is only inspected with find -P / du (neither follows links) and
//     only removed with `rm -rf --one-file-system -- ./<name>`: rm never
//     follows a symlink operand (a link swapped in at the last moment is
//     removed itself, never its target) and GNU rm descends with openat and
//     O_NOFOLLOW, so a link planted inside the tree is not followed either.
//   - Every guard the decision used (name, real directory, age, no process
//     cwd inside) is checked again in the box immediately before each rm.

// DefaultRunCheckoutMaxAge is how old a per-run checkout's newest modification
// must be before the sweep removes it.
const DefaultRunCheckoutMaxAge = 7 * 24 * time.Hour

// runCheckoutSweepInterval is how often every eligible box is swept.
const runCheckoutSweepInterval = time.Hour

// runCheckoutNameERE is the only shape of name the sweep ever touches. It is
// used as a Go regexp and, verbatim, as a grep -E pattern in the box scripts.
const runCheckoutNameERE = `^[A-Za-z0-9._-]+-run-[0-9a-f]{8}$`

var runCheckoutNamePattern = regexp.MustCompile(runCheckoutNameERE)

// runCheckoutScriptPrelude enters <home>/work (the shell variable w) or exits
// quietly when there is none, and prints "X" and exits when the real path is
// not exactly $w (a symlinked ~/work or home). It defines okname, inuse,
// newest and kib for the script that follows. Callers set w first.
const runCheckoutScriptPrelude = `if [ -L "$w" ]; then echo X; exit 0; fi
[ -d "$w" ] || exit 0
cd -P -- "$w" 2>/dev/null || exit 0
[ "$(pwd -P)" = "$w" ] || { echo X; exit 0; }
okname() {
  case "$1" in ''|*[!A-Za-z0-9._-]*) return 1;; esac
  printf '%s\n' "$1" | grep -qxE '` + runCheckoutNameERE + `'
}
inuse() {
  for p in /proc/[0-9]*; do readlink "$p/cwd" 2>/dev/null; done |
    awk -v w="$w/" 'index($0, w) == 1 { s = substr($0, length(w) + 1); sub(/\/.*/, "", s); if (s != "") print s }' | sort -u
}
newest() { find -P "./$1" -xdev -printf '%T@\n' 2>/dev/null | sed 's/\..*//' | sort -n | tail -n 1; }
kib() { du -skx -- "./$1" 2>/dev/null | cut -f1; }
`

// runCheckoutListScript prints one line per entry of home/work whose name
// matches: "C <name> <kind> <newest-epoch|-> <KiB|-> <in-use 0|1>", kind d
// (directory), l (symlink) or o (anything else). Read-only.
func runCheckoutListScript(home string) string {
	return "w=" + shellSingleQuote(home+"/work") + "\n" + runCheckoutScriptPrelude + `u=$(inuse)
for n in * .[!.]* ..?*; do
  [ -e "$n" ] || [ -L "$n" ] || continue
  okname "$n" || continue
  if [ -L "$n" ]; then echo "C $n l - - 0"; continue; fi
  if [ ! -d "$n" ]; then echo "C $n o - - 0"; continue; fi
  busy=0
  printf '%s\n' "$u" | grep -qxF -- "$n" && busy=1
  t=$(newest "$n"); k=$(kib "$n")
  echo "C $n d ${t:--} ${k:--} $busy"
done
`
}

// runCheckoutRemoveScript removes the named entries of home/work, re-checking
// each guard first: the name, a real directory, newest modification strictly
// before cutoff, and no process cwd inside. Prints "R <name>" (removed),
// "K <name>" (no longer qualified, left alone) or "F <name>" (rm failed).
// names must already match runCheckoutNamePattern.
func runCheckoutRemoveScript(home string, names []string, cutoff int64) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = shellSingleQuote(n)
	}
	return "w=" + shellSingleQuote(home+"/work") + "\ncutoff=" + strconv.FormatInt(cutoff, 10) + "\n" + runCheckoutScriptPrelude +
		"for n in " + strings.Join(quoted, " ") + `; do
  okname "$n" || { echo "K $n"; continue; }
  [ -d "$n" ] && [ ! -L "$n" ] || { echo "K $n"; continue; }
  t=$(newest "$n")
  [ -n "$t" ] && [ "$t" -lt "$cutoff" ] || { echo "K $n"; continue; }
  if inuse | grep -qxF -- "$n"; then echo "K $n"; continue; fi
  if rm -rf --one-file-system -- "./$n" 2>/dev/null && [ ! -e "./$n" ] && [ ! -L "./$n" ]; then echo "R $n"; else echo "F $n"; fi
done
`
}

// runCheckoutEntry is one line of runCheckoutListScript's output.
type runCheckoutEntry struct {
	name    string
	kind    byte // 'd', 'l' or 'o'
	newest  time.Time
	hasTime bool
	sizeKiB int64
	inUse   bool
}

// parseRunCheckoutList parses runCheckoutListScript's output. A line that does
// not parse, or names something the pattern does not match, is dropped (never
// a removal candidate). refused reports the X line.
func parseRunCheckoutList(out string) (entries []runCheckoutEntry, refused bool) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 1 && f[0] == "X" {
			refused = true
			continue
		}
		if len(f) != 6 || f[0] != "C" || len(f[2]) != 1 || !runCheckoutNamePattern.MatchString(f[1]) {
			continue
		}
		e := runCheckoutEntry{name: f[1], kind: f[2][0], inUse: f[5] == "1"}
		if secs, err := strconv.ParseInt(f[3], 10, 64); err == nil {
			e.newest, e.hasTime = time.Unix(secs, 0), true
		}
		if k, err := strconv.ParseInt(f[4], 10, 64); err == nil && k >= 0 {
			e.sizeKiB = k
		}
		entries = append(entries, e)
	}
	return entries, refused
}

// runCheckoutLiveRuns is what the sweep knows about the box's live runs.
type runCheckoutLiveRuns struct {
	// since is the earliest start of a RUNNING run; zero when none is.
	since time.Time
	// cwds are the working directories RUNNING runs' records name.
	cwds []string
}

// liveRunsFromList derives runCheckoutLiveRuns from boxRunListScript's output.
// Only RUNNING (same boot, PID alive) counts: an UNKNOWN run's PID is gone, and
// counting one would protect every later checkout until the box reboots.
func liveRunsFromList(out string) (live runCheckoutLiveRuns, records int) {
	views, boot, alive := parseBoxRunViews(out)
	for _, v := range views {
		if agentbox.ResolveOutcome(v.record, boot, func(pid int) bool { return pid > 0 && alive[pid] }) != agentbox.RunOutcomeRunning {
			continue
		}
		if live.since.IsZero() || v.record.StartedAt.Before(live.since) {
			live.since = v.record.StartedAt
		}
		if v.record.Cwd != "" {
			live.cwds = append(live.cwds, v.record.Cwd)
		}
	}
	return live, len(views)
}

// decideRunCheckout is the sweep's whole decision for one entry of home/work.
// cutoff is now minus the maximum age: an entry is removable only when its
