package tracker

import (
	"context"
	"errors"
	"sync"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// A terminal dispatch row whose label projection did not land (#2047,
// #2052). The row is authoritative: an issue whose latest row is
// terminal with labels_pending is not dispatched again, the projection
// is retried every tick until it lands (or a human puts the state label
// on the issue), and the failure comment is posted once per dispatch
// id, never once per tick. Real Postgres store, fake forge whose
// SetLabels always fails (a token that can comment but not label).

const pendingTicks = 5

func allRows(t *testing.T, store *Store, ctx context.Context, user string) []Dispatch {
	t.Helper()
	rows, err := store.ListDispatches(ctx, user, "default", pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_UNSPECIFIED)
	if err != nil {
		t.Fatalf("ListDispatches: %v", err)
	}
	return rows
}

func setLabelErr(p *fakeDispatchProvider, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.labelErr = err
}

// TestDispatch_StartErrorWithPersistentLabelFailureDispatchesOnce is
// #2047's probe: StartRun fails AND the agent:failed write fails, every
// tick. Before the fix each tick re-dispatched the label-less issue —
// one FAILED row and one public failure comment per tick.
func TestDispatch_StartErrorWithPersistentLabelFailureDispatchesOnce(t *testing.T) {
	const user = "tracker-pending-starterr"
	provider := newFakeDispatchProvider(Issue{Number: 21, Labels: []string{"scope:product"}})
	provider.labelErr = errors.New("token may comment but not label")
	runs := &fakeRunStarter{err: errors.New("provisioning failed")}
	d, store, ctx := newDispatchFixture(t, user, provider, runs)

	for i := 0; i < pendingTicks; i++ {
		if _, err := d.Tick(ctx, user, "default"); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
	}
	if n := len(runs.started()); n != 1 {
		t.Fatalf("StartRun calls = %d across %d ticks, want 1", n, pendingTicks)
	}
	rows := allRows(t, store, ctx, user)
	if len(rows) != 1 || rows[0].State != pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_FAILED || !rows[0].LabelsPending {
		t.Fatalf("rows = %+v, want exactly one FAILED row with labels_pending", rows)
	}
	if c := provider.commentsOn(21); len(c) != 1 {
		t.Fatalf("failure comments = %d across %d ticks, want 1 (one per dispatch id)", len(c), pendingTicks)
	}

	// The forge recovers: the retry lands agent:failed and clears the
	// flag — without posting the failure comment a second time.
	setLabelErr(provider, nil)
	res, err := d.Tick(ctx, user, "default")
	if err != nil {
		t.Fatalf("Tick after recovery: %v", err)
	}
	if len(res.Started) != 0 || len(res.Failed) != 0 {
		t.Fatalf("Tick after recovery = %+v, want nothing dispatched", res)
	}
	if labels := provider.labels(21); !containsString(labels, LabelAgentFailed) {
		t.Fatalf("labels = %v, want the retried %s", labels, LabelAgentFailed)
	}
	if row := allRows(t, store, ctx, user)[0]; row.LabelsPending {
		t.Fatalf("row = %+v, want labels_pending cleared once the projection landed", row)
	}
	if c := provider.commentsOn(21); len(c) != 1 {
		t.Fatalf("failure comments = %d, want still 1: the retry re-projects labels, never the comment", len(c))
	}

	// The ordinary re-run protocol now works: a human removes agent:failed.
	runs.mu.Lock()
	runs.err = nil
	runs.mu.Unlock()
	provider.relabel(21, "scope:product")
