package tracker

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Dispatcher core (#2022): fake Provider (in-memory issues), fake
// RunStarter, real Postgres store, injected clock — per the design's
// test strategy.

// fakeDispatchProvider is an in-memory forge: ListIssues returns the
// open issues, SetLabels mutates them, Comment records.
type fakeDispatchProvider struct {
	mu       sync.Mutex
	issues   map[int64]*Issue
	comments map[int64][]string
	listErr  error
	labelErr error
	getErr   error
}

func newFakeDispatchProvider(issues ...Issue) *fakeDispatchProvider {
	f := &fakeDispatchProvider{issues: map[int64]*Issue{}, comments: map[int64][]string{}}
	for i := range issues {
		is := issues[i]
		if is.State == pb.TrackerIssueState_TRACKER_ISSUE_STATE_UNSPECIFIED {
			is.State = pb.TrackerIssueState_TRACKER_ISSUE_STATE_OPEN
		}
		f.issues[is.Number] = &is
	}
	return f
}

func (f *fakeDispatchProvider) ListIssues(_ context.Context, _ Conn, filter IssueFilter) ([]Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []Issue
	for _, is := range f.issues {
		if filter.State != pb.TrackerIssueState_TRACKER_ISSUE_STATE_UNSPECIFIED && is.State != filter.State {
			continue
		}
		cp := *is
		cp.Labels = append([]string(nil), is.Labels...)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

func (f *fakeDispatchProvider) SetLabels(_ context.Context, _ Conn, number int64, add, remove []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.labelErr != nil {
		return f.labelErr
	}
	is := f.issues[number]
	drop := map[string]bool{}
	for _, l := range remove {
		drop[l] = true
	}
	var kept []string
	for _, l := range is.Labels {
		if !drop[l] {
			kept = append(kept, l)
		}
	}
	for _, l := range add {
		if !containsString(kept, l) {
			kept = append(kept, l)
		}
	}
	is.Labels = kept
	return nil
}

func (f *fakeDispatchProvider) Comment(_ context.Context, _ Conn, number int64, body string) (Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments[number] = append(f.comments[number], body)
	return Comment{Body: body}, nil
}

// GetIssue returns the issue's CURRENT state — the dispatcher's
// post-insert re-read (#2023) sees what a peer or a human changed after
// the tick's list.
func (f *fakeDispatchProvider) GetIssue(_ context.Context, _ Conn, number int64) (Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return Issue{}, f.getErr
	}
	is, ok := f.issues[number]
	if !ok {
		return Issue{}, errors.New("no such issue")
	}
	cp := *is
	cp.Labels = append([]string(nil), is.Labels...)
	return cp, nil
}

// relabel replaces an issue's labels, as a human would.
func (f *fakeDispatchProvider) relabel(number int64, labels ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issues[number].Labels = labels
}

func (f *fakeDispatchProvider) labels(number int64) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.issues[number].Labels...)
}

func (f *fakeDispatchProvider) commentsOn(number int64) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.comments[number]...)
}

// fakeRunStarter records every StartRun and optionally fails it.
type fakeRunStarter struct {
	mu    sync.Mutex
	calls []StartRunRequest
	err   error
}

func (f *fakeRunStarter) StartRun(_ context.Context, req StartRunRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	return f.err
}

func (f *fakeRunStarter) started() []StartRunRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]StartRunRequest(nil), f.calls...)
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// newDispatchFixture seeds a connection with a scope:product route and
// returns a Dispatcher over the given fakes.
func newDispatchFixture(t *testing.T, user string, provider *fakeDispatchProvider, runs *fakeRunStarter) (*Dispatcher, *Store, context.Context) {
	t.Helper()
	store, ctx := newTrackerTestStore(t)
	seedDispatchConnection(t, store, ctx, user)
	if _, err := store.SetRoute(ctx, Route{Username: user, Connection: "default", Scope: "product", SkillID: "product-define"}); err != nil {
		t.Fatalf("SetRoute: %v", err)
	}
	return &Dispatcher{
		Store:    store,
		Provider: provider,
		Conn:     Conn{Project: "acme/widgets"},
		Runs:     runs,
		Clock:    newFakeClock(),
	}, store, ctx
}

func TestDispatch_LabeledIssueStartsRunOnce(t *testing.T) {
	const user = "tracker-dispatch-once"
	provider := newFakeDispatchProvider(
		Issue{Number: 42, Title: "idea", Labels: []string{"scope:product"}},
		Issue{Number: 43, Title: "unlabeled"},
	)
	runs := &fakeRunStarter{}
	d, store, ctx := newDispatchFixture(t, user, provider, runs)

	res, err := d.Tick(ctx, user, "default")
	if err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if len(res.Started) != 1 || res.Started[0].IssueNumber != 42 {
		t.Fatalf("Tick 1 started = %+v, want issue 42", res.Started)
	}
	if !containsString(provider.labels(42), LabelAgentQueued) {
		t.Errorf("labels after tick 1 = %v, want %s", provider.labels(42), LabelAgentQueued)
	}

	// Tick 2: the issue now carries agent:queued and has an active row.
	res, err = d.Tick(ctx, user, "default")
	if err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	if len(res.Started) != 0 || res.SkippedActive != 1 {
		t.Fatalf("Tick 2 = %+v, want nothing started, 1 skipped active", res)
	}

	calls := runs.started()
	if len(calls) != 1 {
		t.Fatalf("StartRun calls = %d, want exactly 1", len(calls))
