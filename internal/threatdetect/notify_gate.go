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
