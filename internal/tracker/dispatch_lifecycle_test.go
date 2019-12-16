package tracker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Completion hook (#2023): the dispatch row moves queued -> running when
// the run registers and running -> done|failed when it ends, each a
// compare-and-set, and the forge labels follow the row. Real Postgres
// store, fake forge, fake RunStarter that drives the lifecycle the way
// the production starter does.

// lifecycleRunStarter reports the start synchronously (as the
// production starter does before the in-box agent is launched) and
// keeps each run's lifecycle so a test can end it later — or ends it
// immediately with endWith.
type lifecycleRunStarter struct {
	mu      sync.Mutex
	calls   []StartRunRequest
	endWith *RunOutcome
}

func (l *lifecycleRunStarter) StartRun(ctx context.Context, req StartRunRequest) error {
	l.mu.Lock()
	l.calls = append(l.calls, req)
	end := l.endWith
	l.mu.Unlock()
	if req.Lifecycle != nil {
		if !req.Lifecycle.RunStarted(ctx) {
			return ErrDispatchEnded // as the production starter does
		}
		if end != nil {
			req.Lifecycle.RunEnded(ctx, *end)
		}
	}
	return nil
}

func (l *lifecycleRunStarter) started() []StartRunRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]StartRunRequest(nil), l.calls...)
}

func (l *lifecycleRunStarter) lifecycle(t *testing.T, i int) RunLifecycle {
	t.Helper()
	calls := l.started()
	if len(calls) <= i || calls[i].Lifecycle == nil {
		t.Fatalf("StartRun call %d has no lifecycle (calls=%d)", i, len(calls))
	}
	return calls[i].Lifecycle
}

func newLifecycleFixture(t *testing.T, user string, issues ...Issue) (*Dispatcher, *Store, *fakeDispatchProvider, *lifecycleRunStarter, context.Context) {
	t.Helper()
	provider := newFakeDispatchProvider(issues...)
	runs := &lifecycleRunStarter{}
	d, store, ctx := newDispatchFixture(t, user, provider, &fakeRunStarter{})
	d.Runs = runs
	return d, store, provider, runs, ctx
}

func onlyRow(t *testing.T, store *Store, ctx context.Context, user string) Dispatch {
	t.Helper()
	rows, err := store.ListDispatches(ctx, user, "default", pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_UNSPECIFIED)
	if err != nil {
		t.Fatalf("ListDispatches: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want exactly one", rows)
	}
	return rows[0]
}

// TestDispatchLifecycle_QueuedRunningDone: the happy path. The run
// registering moves the row to RUNNING and the issue to agent:running
// (agent:queued removed); the run ending cleanly moves the row to DONE,
// the issue to agent:done, and removes the trigger scope label.
func TestDispatchLifecycle_QueuedRunningDone(t *testing.T) {
	const user = "tracker-lifecycle-done"
	d, store, provider, runs, ctx := newLifecycleFixture(t, user, Issue{Number: 42, Labels: []string{"scope:product", "bug"}})

	res, err := d.Tick(ctx, user, "default")
	if err != nil || len(res.Started) != 1 {
		t.Fatalf("Tick = (%+v, %v), want one started", res, err)
	}
	if got := res.Started[0].State; got != pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_RUNNING {
		t.Errorf("started row state = %v, want RUNNING (the run registered)", got)
	}
	row := onlyRow(t, store, ctx, user)
	if row.State != pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_RUNNING || row.StartedAt.IsZero() {
		t.Fatalf("row = %+v, want RUNNING with started_at", row)
	}
	labels := provider.labels(42)
	if !containsString(labels, LabelAgentRunning) || containsString(labels, LabelAgentQueued) {
		t.Fatalf("labels while running = %v, want %s and not %s", labels, LabelAgentRunning, LabelAgentQueued)
	}

	runs.lifecycle(t, 0).RunEnded(ctx, RunOutcome{})

	row = onlyRow(t, store, ctx, user)
	if row.State != pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_DONE || row.EndedAt.IsZero() || row.LabelsPending {
		t.Fatalf("row = %+v, want DONE with ended_at and no labels pending", row)
	}
	labels = provider.labels(42)
	if !containsString(labels, LabelAgentDone) {
		t.Errorf("labels = %v, want %s", labels, LabelAgentDone)
	}
	for _, gone := range []string{LabelAgentRunning, LabelAgentQueued, "scope:product"} {
		if containsString(labels, gone) {
			t.Errorf("labels = %v, want %s removed on done", labels, gone)
		}
	}
	if !containsString(labels, "bug") {
		t.Errorf("labels = %v, an unrelated label must survive", labels)
	}
	// The result comment is the run's own job; the dispatcher says
	// nothing on success.
	if c := provider.commentsOn(42); len(c) != 0 {
		t.Errorf("dispatcher comments on success = %q, want none", c)
	}
}

// TestDispatchLifecycle_RunningFailedWithComment: a run that ends with
// an error moves RUNNING -> FAILED, labels agent:failed, keeps the
// scope label (a human retries by removing agent:failed), and posts one
// stamped comment naming the run and dispatch — never the raw error.
func TestDispatchLifecycle_RunningFailedWithComment(t *testing.T) {
	const user = "tracker-lifecycle-failed"
	d, store, provider, runs, ctx := newLifecycleFixture(t, user, Issue{Number: 5, Labels: []string{"scope:product"}})

	if res, err := d.Tick(ctx, user, "default"); err != nil || len(res.Started) != 1 {
		t.Fatalf("Tick = (%+v, %v)", res, err)
	}
	runs.lifecycle(t, 0).RunEnded(ctx, RunOutcome{Err: errors.New("agent exploded at 10.0.0.9 for tenant secret-tenant")})

	row := onlyRow(t, store, ctx, user)
	if row.State != pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_FAILED || row.EndedAt.IsZero() ||
		!strings.Contains(row.FailureReason, "agent exploded") {
		t.Fatalf("row = %+v, want FAILED with the reason recorded on the row", row)
	}
	labels := provider.labels(5)
	if !containsString(labels, LabelAgentFailed) || containsString(labels, LabelAgentRunning) || containsString(labels, LabelAgentQueued) {
		t.Errorf("labels = %v, want %s only among state labels", labels, LabelAgentFailed)
	}
