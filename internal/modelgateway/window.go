package modelgateway

import (
	"sort"
	"sync"
	"time"
)

// Rolling-window accounting shared by the quota check (how much has this tenant
// spent recently?) and the anomaly signals (how does recent behavior compare to
// this tenant's own history?).
//
// Both need "the last N minutes", not "since process start", so a fixed counter
// is the wrong shape: a tenant that blew its budget an hour ago should be able
// to work again. A true sliding window would need per-request timestamps and
// unbounded memory, so this approximates one with a ring of fixed buckets —
// the standard trade. Resolution is window/bucketCount; a tenant can overshoot
// by at most one bucket's worth at a window boundary, which for a spend guard
// is the right way to be wrong (bounded, and in the generous direction).

// bucketCount is the ring size. Twelve buckets put the resolution at 5s for a
// 1-minute window and 5m for an hour — fine enough that boundary overshoot is
// small, coarse enough that the ring stays cheap to sum on every request.
const bucketCount = 12

// counts is one bucket's accumulated usage. Numeric only: categorical signals
// (which models, which paths, which source networks) live in recentSet, which
// has different eviction semantics.
type counts struct {
	calls        int64
	inputTokens  int64
	outputTokens int64
	cachedTokens int64

	// authDenials are requests rejected because the token did not authorize
	// them — wrong provider, model outside the allowed set. These are evidence
	// about the CALLER, which is why the auth-failure-ratio signal reads them.
	authDenials int64

	// policyDenials are requests the ladder itself refused. Deliberately a
	// separate counter: folding them into authDenials would make enforcement
	// self-amplifying — throttling a tenant would raise its own failure ratio,
	// which raises its anomaly score, which escalates it further, until a
	// budget overrun had been laundered into "this token is being probed".
	policyDenials int64
}

func (c *counts) add(o counts) {
	c.calls += o.calls
	c.inputTokens += o.inputTokens
	c.outputTokens += o.outputTokens
	c.cachedTokens += o.cachedTokens
	c.authDenials += o.authDenials
	c.policyDenials += o.policyDenials
}

// totalTokens is what a token budget is spent against. Cached tokens are
// counted: they cost money (at a discount the gateway doesn't model) and, more
// to the point, a runaway agent replaying a huge cached prefix is exactly the
// spend pattern a quota exists to stop.
func (c counts) totalTokens() int64 {
	return c.inputTokens + c.outputTokens + c.cachedTokens
}

// rollingWindow is a ring of time buckets covering `window`. Not safe for
// concurrent use on its own — callers hold the owning tenantState's lock.
type rollingWindow struct {
	window  time.Duration
	slot    time.Duration
	buckets [bucketCount]counts
	// starts[i] is the bucket's start time, used to decide whether the bucket
	// is live or a stale one that must be zeroed before reuse.
	starts [bucketCount]time.Time
}

func newRollingWindow(window time.Duration) *rollingWindow {
	if window <= 0 {
		window = time.Minute
	}
	return &rollingWindow{window: window, slot: window / bucketCount}
}

// index returns the ring slot for t, zeroing it first if it holds a bucket from
// an earlier revolution. This is what makes old usage age out without a sweeper.
func (w *rollingWindow) index(t time.Time) int {
	if w.slot <= 0 {
		w.slot = time.Nanosecond
	}
	n := t.UnixNano() / int64(w.slot)
	i := int(n % bucketCount)
	if i < 0 {
		i += bucketCount
	}
