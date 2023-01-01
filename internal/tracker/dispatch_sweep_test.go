package tracker

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Failure visibility (#2026): a run that errors, loses its lease, or
// outlives the connection policy's run timeout ends FAILED with a typed
// cause, agent:failed, and one comment naming the run and the reason;
// every terminal transition is observed once for the PRD's metrics.
// Real Postgres store, fake forge, fake leases, injected clock.

// fakeLeases is the daemon's live-run view: a run is live until ended.
type fakeLeases struct {
	mu    sync.Mutex
	live  map[string]bool
	ended []string
	// onEnd runs inside End, before it returns (a test hook: cancel the
	// tick, panic, or record ordering).
	onEnd func(runID string)
}

func newFakeLeases() *fakeLeases { return &fakeLeases{live: map[string]bool{}} }

func (f *fakeLeases) Live(runID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live[runID]
}

func (f *fakeLeases) End(_ context.Context, runID string) {
	f.mu.Lock()
	f.ended = append(f.ended, runID)
	delete(f.live, runID)
	hook := f.onEnd
	f.mu.Unlock()
	if hook != nil {
		hook(runID)
	}
}

func (f *fakeLeases) setLive(runID string, live bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live[runID] = live
}

func (f *fakeLeases) endedRuns() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ended...)
}

// fakeObserver records every terminal event.
type fakeObserver struct {
	mu     sync.Mutex
	events []DispatchEnded
}

func (f *fakeObserver) DispatchEnded(ev DispatchEnded) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *fakeObserver) all() []DispatchEnded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]DispatchEnded(nil), f.events...)
}

// ctxProvider makes every forge write honour its context, so a test can
// prove the bookkeeping runs on a detached one.
type ctxProvider struct{ *fakeDispatchProvider }

func (c ctxProvider) SetLabels(ctx context.Context, conn Conn, n int64, add, remove []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.fakeDispatchProvider.SetLabels(ctx, conn, n, add, remove)
}

func (c ctxProvider) Comment(ctx context.Context, conn Conn, n int64, body string) (Comment, error) {
	if err := ctx.Err(); err != nil {
		return Comment{}, err
	}
	return c.fakeDispatchProvider.Comment(ctx, conn, n, body)
}

const sweepTimeout = time.Hour

// newSweepFixture: one routed issue, dispatched and RUNNING, its run
// live in fakeLeases.
func newSweepFixture(t *testing.T, user string) (*Dispatcher, *Store, *fakeDispatchProvider, *lifecycleRunStarter, *fakeLeases, *fakeObserver, context.Context) {
	t.Helper()
	d, store, provider, runs, ctx := newLifecycleFixture(t, user, Issue{Number: 7, Labels: []string{"scope:product"}})
	leases, obs := newFakeLeases(), &fakeObserver{}
	d.Policy = &Policy{RunTimeout: sweepTimeout}
	d.Leases, d.Observer = leases, obs
	res, err := d.Tick(ctx, user, "default")
	if err != nil || len(res.Started) != 1 {
		t.Fatalf("Tick = (%+v, %v), want one started", res, err)
	}
	leases.setLive(res.Started[0].RunID, true)
	return d, store, provider, runs, leases, obs, ctx
}

func clockOf(t *testing.T, d *Dispatcher) *fakeClock {
	t.Helper()
	c, ok := d.Clock.(*fakeClock)
	if !ok {
		t.Fatalf("clock is %T, want *fakeClock", d.Clock)
	}
	return c
}

// assertFailedOnce checks the row is FAILED with the given cause, the
// issue carries only agent:failed among state labels, and exactly one
// dispatcher comment names the run and says why.
func assertFailedOnce(t *testing.T, store *Store, provider *fakeDispatchProvider, ctx context.Context, user string, issue int64, want pb.TrackerDispatchFailure, reasonText string) Dispatch {
	t.Helper()
	row := onlyRow(t, store, ctx, user)
	if row.State != pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_FAILED || row.Failure != want || row.EndedAt.IsZero() {
		t.Fatalf("row = %+v, want FAILED with failure %v and ended_at", row, want)
	}
	labels := provider.labels(issue)
	if !containsString(labels, LabelAgentFailed) || containsString(labels, LabelAgentRunning) || containsString(labels, LabelAgentQueued) {
		t.Errorf("labels = %v, want %s only among state labels", labels, LabelAgentFailed)
	}
	comments := provider.commentsOn(issue)
	if len(comments) != 1 {
		t.Fatalf("comments = %q, want exactly one failure comment", comments)
	}
	for _, want := range []string{row.RunID, row.ID, reasonText} {
		if !strings.Contains(comments[0], want) {
			t.Errorf("comment = %q, want it to contain %q", comments[0], want)
		}
	}
	if _, _, kind, ok := ParseMarker(comments[0]); !ok || kind != KindComment {
		t.Errorf("comment = %q, want a platform identity stamp", comments[0])
	}
	return row
}

// A RUNNING row past the run timeout is failed by the next tick: lease
// ended, agent:failed, one comment with the run id and "timed out", one
// TIMEOUT event. The run's own late RunEnded and later ticks change
// nothing — no double report.
func TestDispatchSweep_TimeoutFailsRunningRowOnce(t *testing.T) {
	const user = "tracker-sweep-timeout"
	d, store, provider, runs, leases, obs, ctx := newSweepFixture(t, user)
	clock := clockOf(t, d)

	// Just inside the timeout: untouched.
	clock.Advance(sweepTimeout - time.Second)
	res, err := d.Tick(ctx, user, "default")
	if err != nil || len(res.TimedOut) != 0 {
		t.Fatalf("Tick inside timeout = (%+v, %v), want nothing timed out", res, err)
	}

	// Labels must follow the lease end, never precede it: the issue may
	// not say agent:failed while the run's credentials are still live.
	leases.onEnd = func(string) {
		if containsString(provider.labels(7), LabelAgentFailed) {
			t.Errorf("agent:failed projected before the lease was ended")
		}
	}
	clock.Advance(2 * time.Second)
	res, err = d.Tick(ctx, user, "default")
	if err != nil {
		t.Fatalf("Tick past timeout: %v", err)
	}
	if len(res.TimedOut) != 1 || res.TimedOut[0].Failure != pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_TIMEOUT {
		t.Fatalf("TimedOut = %+v, want one TIMEOUT row", res.TimedOut)
	}
	row := assertFailedOnce(t, store, provider, ctx, user, 7, pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_TIMEOUT, "timed out after 1h0m0s")
	if got := leases.endedRuns(); len(got) != 1 || got[0] != row.RunID {
		t.Errorf("leases ended = %v, want exactly [%s]", got, row.RunID)
	}

	// The run finally returns: its RunEnded loses the compare-and-set.
	runs.lifecycle(t, 0).RunEnded(ctx, RunOutcome{})
	// And later ticks do not sweep a terminal row again.
	for i := 0; i < 2; i++ {
		clock.Advance(sweepTimeout)
		if res, err := d.Tick(ctx, user, "default"); err != nil || len(res.TimedOut) != 0 {
			t.Fatalf("later Tick %d = (%+v, %v), want nothing timed out", i, res, err)
		}
	}
	assertFailedOnce(t, store, provider, ctx, user, 7, pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_TIMEOUT, "timed out")
	if got := leases.endedRuns(); len(got) != 1 {
		t.Errorf("leases ended = %v, want exactly one End", got)
	}

	evs := obs.all()
	if len(evs) != 1 {
		t.Fatalf("observer events = %+v, want exactly one", evs)
	}
	ev := evs[0]
	if ev.State != pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_FAILED || ev.Failure != pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_TIMEOUT || ev.Scope != "product" {
		t.Errorf("event = %+v, want FAILED/TIMEOUT for scope product", ev)
	}
	if want := sweepTimeout + time.Second; ev.Latency != want {
		t.Errorf("latency = %v, want %v (label applied -> result)", ev.Latency, want)
	}
}

// A RUNNING row whose run has no live lease (the daemon restarted) is
// failed LEASE_LOST once the grace period is over — not before, and not
// while the lease is live.
func TestDispatchSweep_LeaseLostAfterGrace(t *testing.T) {
	const user = "tracker-sweep-leaselost"
	d, store, provider, _, leases, obs, ctx := newSweepFixture(t, user)
	clock := clockOf(t, d)
	row := onlyRow(t, store, ctx, user)

	// Live run, well past the grace: untouched.
	clock.Advance(DefaultLeaseLostGrace + time.Minute)
	if res, err := d.Tick(ctx, user, "default"); err != nil || len(res.TimedOut) != 0 {
		t.Fatalf("Tick with live lease = (%+v, %v), want nothing swept", res, err)
	}

	// The daemon "restarts": the lease is gone. The grace is measured
	// from the start, which is already past it.
	leases.setLive(row.RunID, false)
	res, err := d.Tick(ctx, user, "default")
	if err != nil || len(res.TimedOut) != 1 {
		t.Fatalf("Tick after lease loss = (%+v, %v), want one swept", res, err)
	}
	assertFailedOnce(t, store, provider, ctx, user, 7, pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_LEASE_LOST, "no live lease")
	if evs := obs.all(); len(evs) != 1 || evs[0].Failure != pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_LEASE_LOST {
		t.Errorf("observer events = %+v, want one LEASE_LOST", evs)
	}
}

// A QUEUED row with no run behind it (a daemon that died mid-tick) is
// swept after the grace period instead of locking the issue forever,
// and a human can then re-run the issue.
func TestDispatchSweep_StrandedQueuedRowIsFreed(t *testing.T) {
	const user = "tracker-sweep-queued"
	provider := newFakeDispatchProvider(Issue{Number: 3, Labels: []string{"scope:product", LabelAgentQueued}})
	runs := &lifecycleRunStarter{}
	d, store, ctx := newDispatchFixture(t, user, provider, &fakeRunStarter{})
	d.Runs, d.Leases = runs, newFakeLeases()
	d.Policy = &Policy{RunTimeout: sweepTimeout}
	clock := clockOf(t, d)
	if _, err := store.InsertDispatch(ctx, Dispatch{
		Username: user, Connection: "default", IssueNumber: 3, Scope: "product", SkillID: "product-define",
		RunID: "run-stranded", CreatedAt: clock.Now(),
	}); err != nil {
		t.Fatalf("InsertDispatch: %v", err)
	}

	// Within the grace: a peer's tick may still be starting it.
	clock.Advance(DefaultLeaseLostGrace - time.Second)
	if res, err := d.Tick(ctx, user, "default"); err != nil || len(res.TimedOut) != 0 {
		t.Fatalf("Tick within grace = (%+v, %v), want nothing swept", res, err)
	}
	clock.Advance(2 * time.Second)
	if res, err := d.Tick(ctx, user, "default"); err != nil || len(res.TimedOut) != 1 {
		t.Fatalf("Tick past grace = (%+v, %v), want the stranded row swept", res, err)
	}
	assertFailedOnce(t, store, provider, ctx, user, 3, pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_LEASE_LOST, "run-stranded")

	// The issue is no longer locked: a human retries, the next tick runs it.
	provider.relabel(3, "scope:product")
	res, err := d.Tick(ctx, user, "default")
	if err != nil || len(res.Started) != 1 {
		t.Fatalf("Tick after retry = (%+v, %v), want the issue dispatched again", res, err)
	}
}

// listBarrierStore holds every caller of the first `parties`
// ListDispatches(RUNNING) calls until all of them have listed, so
// concurrent sweeps are guaranteed to have read the same active rows
// before either one fails them — the race the compare-and-set decides.
type listBarrierStore struct {
	DispatchStore
	parties int

	mu      sync.Mutex
	arrived int
	release chan struct{}
}

func newListBarrierStore(s DispatchStore, parties int) *listBarrierStore {
	return &listBarrierStore{DispatchStore: s, parties: parties, release: make(chan struct{})}
}

