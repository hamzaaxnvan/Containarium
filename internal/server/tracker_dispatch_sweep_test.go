package server

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Timeout / lease-lost sweep, server half (#2026): the production
// RunStarter is also the dispatcher's view of live runs. A dispatched
// run is live from StartRun until its end has been reported — not just
// until its lease ended — so the sweep never mistakes a run that is
// finishing for a lost one; the sweep can end a live run's lease, and
// the run's own end then does not end it a second time; and a panic or
// runtime.Goexit in the run's background half still stops it being
// live.

// countingRevoker counts every revocation call.
type countingRevoker struct {
	*fakeRevocationStore
	mu sync.Mutex
	n  int
}

func (c *countingRevoker) Revoke(ctx context.Context, jti string, exp time.Time, reason string) error {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.fakeRevocationStore.Revoke(ctx, jti, exp, reason)
}

func (c *countingRevoker) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

var _ tracker.RunLeases = trackerRunStarter{}

func waitNotLive(t *testing.T, leases tracker.RunLeases, runID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for leases.Live(runID) {
		if time.Now().After(deadline) {
			t.Fatalf("run %s still live after its end was reported", runID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The sweep ends a live run's lease (both JWTs revoked); when the run's
// agent finally returns, its lease is not ended again, its end is still
// reported, and only then does it stop being live.
func TestTrackerRunLeases_SweepEndsLeaseOnce(t *testing.T) {
	const runID = "run-sweep-once"
	s, _, store := newFinishHarness(t, runID)
	rev := &countingRevoker{fakeRevocationStore: store}
	s.SetRevocationStore(rev)
	leases := trackerRunStarter{s}

	release := make(chan struct{})
	lc := newRecordingLifecycle()
	s.launchDispatchedRun(ctxAs("alice", true), &startedSkillRun{runID: runID, lease: testLease(runID)}, lc,
		func(string, string) (string, error) { <-release; return "{}", nil })
	if !leases.Live(runID) {
		t.Fatal("a launched run is not live")
	}

	leases.End(context.Background(), runID) // the timeout sweep
	if got := rev.calls(); got != 2 {
		t.Fatalf("revocations after the sweep's End = %d, want 2 (both JWTs)", got)
	}
	if !leases.Live(runID) {
		t.Error("run stopped being live before its end was reported")
	}

	close(release)
	<-lc.ended
	waitNotLive(t, leases, runID)
	if got := rev.calls(); got != 2 {
		t.Errorf("revocations after the run ended = %d, want still 2 (the lease ends once)", got)
	}
	leases.End(context.Background(), runID) // a late sweep: a no-op
	if got := rev.calls(); got != 2 {
		t.Errorf("revocations after a late End = %d, want still 2", got)
	}
}

// The other ordering: the run's own end comes first and the sweep's End
// arrives while that end is still being reported (the run is still
// live). The lease was already ended by the run, so the sweep's End must
// not revoke again.
func TestTrackerRunLeases_RunEndFirstThenSweepEndsOnce(t *testing.T) {
	const runID = "run-end-first"
	s, _, store := newFinishHarness(t, runID)
	rev := &countingRevoker{fakeRevocationStore: store}
	s.SetRevocationStore(rev)
	leases := trackerRunStarter{s}

	var liveAtSweep bool
	lc := &callbackLifecycle{onEnd: func() {
		liveAtSweep = leases.Live(runID)
		leases.End(context.Background(), runID) // the sweep lands mid-report
	}}
	done := make(chan struct{})
	lc.after = func() { close(done) }
	s.launchDispatchedRun(ctxAs("alice", true), &startedSkillRun{runID: runID, lease: testLease(runID)}, lc,
		func(string, string) (string, error) { return "{}", nil })
	<-done
	if !liveAtSweep {
		t.Fatal("run not live while its end was reported; the sweep's End took the not-live path and the ordering is untested")
	}
	waitNotLive(t, leases, runID)
	if got := rev.calls(); got != 2 {
		t.Errorf("revocations = %d, want 2 (the run's own end only; the sweep's End is a no-op)", got)
	}
}

// Security (#2026): a run the sweep timed out while it was still
