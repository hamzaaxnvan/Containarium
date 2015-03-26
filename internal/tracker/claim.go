package tracker

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RunResolver answers "is this run still going" — backed by run
// leases (internal/runlease.Registry satisfies this). See its own
// doc comment for what its single-process, non-durable scope means
// for ClaimTrackerIssue's correctness.
type RunResolver interface {
	Live(runID string) bool
}

// Clock is the seam ClaimTrackerIssue reads the current time through,
// so a test can control what counts as "stale" without a real sleep.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production Clock.
var SystemClock Clock = systemClock{}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// DefaultStaleAfter is the design note's default: a claim marker from
// a run this daemon doesn't know about (e.g. made through another
// daemon instance) is still honored for this long before being
// considered abandoned and takeable.
const DefaultStaleAfter = 2 * time.Hour

// ClaimLocks serializes ClaimTrackerIssue calls per (username,
// connection, issue) within this daemon — the design note's "claims
// are serialized per (connection, issue) within the daemon." Callers
// must hold the lock for the full duration of one ClaimTrackerIssue
// call; the cross-daemon race that can still happen despite the lock
// (a different daemon instance racing this one) is what the
// post-claim re-read step guards against instead.
type ClaimLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewClaimLocks returns an empty ClaimLocks.
func NewClaimLocks() *ClaimLocks {
	return &ClaimLocks{locks: make(map[string]*sync.Mutex)}
}

// Lock returns the mutex for (username, connection, number), creating
// it on first use. Locks are never removed — a per-(tenant,
// connection, issue) mutex is a few dozen bytes and this daemon's
// tracker traffic is, per the architecture doc's own stated scale ("a
// handful of concurrent agent runs per tenant"), nowhere near where
// that adds up.
func (c *ClaimLocks) Lock(username, connection string, number int64) *sync.Mutex {
	key := fmt.Sprintf("%s/%s#%d", username, connection, number)
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.locks[key]
	if !ok {
		l = &sync.Mutex{}
		c.locks[key] = l
	}
	return l
}

// ClaimResult is the outcome of ClaimTrackerIssue.
type ClaimResult struct {
	// Claimed is true when THIS run holds the claim once the call
	// returns — either it just claimed the issue, took over a stale
	// claim, or already held the claim (idempotent re-claim).
	Claimed bool
	// AlreadyClaimedByRunID is set when Claimed is false: the run id
	// that holds a live-or-fresh-enough claim instead.
	AlreadyClaimedByRunID string
	// Assigned is true if AssignIfUnassigned actually changed the
	// issue's assignee as part of this call.
	Assigned bool
}

// ClaimTrackerIssue implements the design note's claim algorithm.
// Callers must hold the ClaimLocks entry for (id's tenant, the
// connection, number) for the duration of this call — it assumes no
// other goroutine in this process is claiming the same issue
// concurrently; the cross-daemon case is handled by the re-read step
// below instead.
//
//  1. Read the issue's comments; find the newest kind=claim marker.
//     Absent, or from THIS run (id.RunID) → proceed to claim (a
//     same-run re-claim is idempotent: found and matching, nothing is
//     posted, this returns immediately with Claimed=true).
//  2. From a DIFFERENT run: if that run is resolver.Live, or its
//     marker is younger than staleAfter, return AlreadyClaimedByRunID
//     — no write. Otherwise it's stale and unowned; fall through.
//  3. Post the claim comment, then AssignIfUnassigned (never replaces
//     an existing assignee — correct on GitLab Free's single-assignee
//     model).
//  4. Re-read. If a foreign claim with a timestamp EARLIER than the
//     one just posted is now visible (a different daemon instance won
//     a race in the gap between this call's read and its post) and
//     that run is live-or-fresh, post a kind=yield comment (marking
//     THIS run's own just-posted claim as superseded — see
//     latestClaim) and report AlreadyClaimedByRunID instead of
//     Claimed.
//
// Every marker considered anywhere in this algorithm must also have
// been posted by the connection's OWN credential (resolved once via
// provider.WhoAmI) — caught in review of #1924 (CWE-345): the marker
// syntax is public and trivially reproducible by any tracker commenter,
// so trusting a marker's kind/run-id claims without checking who
// actually posted the comment would let anyone force a false
// ALREADY_CLAIMED. Every genuine claim/yield — even one made through a
// DIFFERENT daemon instance — shares this same identity, since every
