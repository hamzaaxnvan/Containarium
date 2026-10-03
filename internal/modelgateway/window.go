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
	start := time.Unix(0, n*int64(w.slot))
	if !w.starts[i].Equal(start) {
		w.buckets[i] = counts{}
		w.starts[i] = start
	}
	return i
}

// add accumulates c into the bucket covering now.
func (w *rollingWindow) add(now time.Time, c counts) {
	w.buckets[w.index(now)].add(c)
}

// sum totals every bucket still inside the window as of now. Buckets older than
// the window are skipped rather than zeroed — index() clears them lazily on
// reuse, so a tenant that goes quiet costs nothing until it returns.
func (w *rollingWindow) sum(now time.Time) counts {
	var out counts
	cutoff := now.Add(-w.window)
	for i := range w.buckets {
		if w.starts[i].IsZero() || w.starts[i].Before(cutoff) {
			continue
		}
		out.add(w.buckets[i])
	}
	return out
}

// rate returns tokens-per-second over the window as of now, using the elapsed
// span actually covered by live buckets rather than the nominal window — so a
// tenant three seconds into a one-minute window reports its real rate instead of
// one twentieth of it. That distinction is the whole point for the baseline
// comparison: without it every burst looks like a slow ramp.
func (w *rollingWindow) rate(now time.Time) float64 {
	var (
		out     counts
		oldest  time.Time
		cutoff  = now.Add(-w.window)
		haveAny bool
	)
	for i := range w.buckets {
		if w.starts[i].IsZero() || w.starts[i].Before(cutoff) {
			continue
		}
		out.add(w.buckets[i])
		if !haveAny || w.starts[i].Before(oldest) {
			oldest, haveAny = w.starts[i], true
		}
	}
	if !haveAny {
		return 0
	}
	elapsed := now.Sub(oldest).Seconds()
	// Floor at one slot: a single in-progress bucket would otherwise divide by
	// a near-zero elapsed and report an absurd rate on the first request.
	if min := w.slot.Seconds(); elapsed < min {
		elapsed = min
	}
	if elapsed <= 0 {
		return 0
	}
	return float64(out.totalTokens()) / elapsed
}

// maxRecentValues caps how many distinct values one tenant's set retains.
//
// This is a hard bound, not a tuning knob. Two of the three sets are keyed on
// caller-controlled input — the upstream path and the request's model id come
// straight off the wire — so an unbounded map would let one box grow a tenant's
// state without limit for the whole novelty TTL (24h by default), and make the
// per-observation prune scan cost grow with it. 64 is far above what a real box
// exercises (a handful of endpoints, a handful of models) and low enough that
// the linear scans below are trivial.
const maxRecentValues = 64

// recentSet tracks which distinct categorical values a tenant has used lately —
// model ids, upstream paths, source networks. Values expire by last-seen rather
// than by bucket, because the question these answer is "is this one new?", not
// "how many times?".
//
// Bounded at maxRecentValues: past the cap the least-recently-seen value is
// evicted. An evicted value reads as novel if it returns, which is the honest
// answer — it is no longer among the tenant's recent behavior. A caller
// flooding distinct values evicts its own history and keeps tripping the
// novelty signal, which is the correct outcome rather than a bypass.
type recentSet struct {
	ttl  time.Duration
	seen map[string]time.Time
}

func newRecentSet(ttl time.Duration) *recentSet {
	return &recentSet{ttl: ttl, seen: map[string]time.Time{}}
}

// observe records v and reports whether it was absent (or expired) — i.e.
// whether this is a value the tenant has not used within the ttl.
func (s *recentSet) observe(now time.Time, v string) bool {
	if v == "" {
		return false
	}
	s.prune(now)
	last, ok := s.seen[v]
	s.seen[v] = now
	if !ok && len(s.seen) > maxRecentValues {
		s.evictOldest()
	}
	return !ok || now.Sub(last) > s.ttl
}

// evictOldest drops the least-recently-seen entry. Linear, but bounded by
// maxRecentValues and only reached when the set is full.
func (s *recentSet) evictOldest() {
	var (
		oldestKey string
		oldestAt  time.Time
		first     = true
	)
	for k, t := range s.seen {
		if first || t.Before(oldestAt) {
			oldestKey, oldestAt, first = k, t, false
		}
	}
	if !first {
		delete(s.seen, oldestKey)
	}
}

// len is the number of distinct live values.
func (s *recentSet) len(now time.Time) int {
	s.prune(now)
	return len(s.seen)
}

// values returns the live values in sorted order (stable output for the ops
// readout and for tests).
func (s *recentSet) values(now time.Time) []string {
	s.prune(now)
	out := make([]string, 0, len(s.seen))
	for v := range s.seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func (s *recentSet) prune(now time.Time) {
	for v, t := range s.seen {
		if now.Sub(t) > s.ttl {
			delete(s.seen, v)
		}
	}
}

// ewma is an exponentially-weighted moving average with a sample counter, used
// for the per-tenant baseline. The counter matters as much as the average: a
// baseline built from two samples is not a baseline, and comparing against it
// produces exactly the false positives that make anomaly detection get switched
// off. Callers gate on ready().
type ewma struct {
	mu      sync.Mutex
	alpha   float64
	value   float64
	samples int
}

func newEWMA(alpha float64) *ewma {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.2
	}
	return &ewma{alpha: alpha}
}

func (e *ewma) observe(v float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.samples == 0 {
		e.value = v
	} else {
		e.value = e.alpha*v + (1-e.alpha)*e.value
	}
	e.samples++
}

func (e *ewma) get() (float64, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.value, e.samples
}
