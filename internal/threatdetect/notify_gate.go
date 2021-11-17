package threatdetect

import (
	"maps"
	"sync"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// DefaultRenotifyInterval is how long an open finding stays quiet on the
// webhook after a delivery before a repeat of the same condition is
// delivered again (#2643).
const DefaultRenotifyInterval = time.Hour

// gateMaxEntries bounds the per-finding memory. A finding that never resolves
// keeps its entry; past the cap the oldest entry goes, which at worst
// re-delivers that one finding early.
const gateMaxEntries = 4096

// deliveryGate decides which store writes reach the webhook. The stores call
// Notify on every write, including the dedupe path that only bumps count on
// an already-open (rule, tenant, subject); delivering all of those turns one
// persistent condition into an alert every evaluation cycle. The row dedupes
// (#1640); this makes the delivery dedupe too.
//
// A finding is delivered when it is new to the gate, when its severity
// escalates, when a box's response state changes (#2718: NONE→APPLIED,
// APPLIED→RELEASED/EXPIRED, anything→FAILED — what the process responder did
// is news, not a repeat), when its kill outcome changes (#2775: the first kill
// and the first skipped kill at once, later ones at most once per
// KillRenotifyInterval with the latest pid and the kill count), when interval has passed since its last delivery,
// and once when it resolves. State is in memory: after a restart an open
// finding is delivered again on its next write, which is a re-notify, not a
// storm.
//
// Response state is tracked per box (#2820): a miner-signature finding is
// keyed per tenant and pattern, so several boxes of one tenant can share it,
// and each write names the box it is about in FindingResponse.Container.
// Box B's APPLIED after box A's is B's transition, not a repeat of A's, and
// so is each box's release. Severity, the kill outcome and the re-notify
// interval stay per finding: a kill count is the finding's, so a respawn
// storm across boxes is still bounded by KillRenotifyInterval.
//
// Every resolve is delivered. An operator release that resolves the finding
// is one delivery, not two, because the store records the release and the
// resolve in one write (ResponseWriter.ResolveWithResponse) whose delivery
// is the resolve carrying the RELEASED record; nothing here needs to drop a
// resolve as the second half of a release.
type deliveryGate struct {
	mu       sync.Mutex
	interval time.Duration
	now      func() time.Time
	last     map[int64]gateEntry
}

type gateEntry struct {
	at       time.Time
	severity pb.ThreatSeverity
	// boxes is the response state last delivered per box
	// (FindingResponse.Container; "" for a response that names none). A box
	// not in it has had no response delivered: NONE.
	boxes  map[string]pb.ResponseState
	kill   killKey
	killAt time.Time // when a changed kill outcome was last delivered
	// skipSent: a skipped kill has been delivered for this finding. The
	// "first skip is immediate" fast path fires once per finding; a delivered
	// kill clears the skip on the delivered entry, so the entry's kill alone
	// cannot tell a first skip from a later one (#2775 review).
	skipSent bool
}

// KillRenotifyInterval bounds the webhook posts a respawning miner can cause
// (#2775): the first kill and the first skipped kill of a finding are
// delivered at once, and any later change in its kill outcome (another pid,
// a higher kill count, another skip reason) at most once per interval,
// carrying the latest pid and the count. A change that lands inside the
// interval is held back until the finding's next write after it, so the
// webhook never sees one post per kill.
const KillRenotifyInterval = time.Minute

// killKey is the kill outcome on a finding's response: a change in it is news
// (#2775). Under ALERT a kill leaves the response state at NONE, so the state
// alone never shows it.
type killKey struct {
	killed bool
	pid    uint32
	kills  uint32
	skip   pb.KillSkipReason
}

func killOf(f *Finding) killKey {
	if f.Response == nil {
		return killKey{}
	}
	r := f.Response
	return killKey{r.ProcessKilled, r.KilledPID, r.Kills, r.KillSkipReason}
}

// killNews reports whether f's kill outcome should reach the webhook now,
// given what was last delivered for it. Skipped kills are keyed on the reason
// kind, never on text, so a process that exits before every kill is one
