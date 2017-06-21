package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Stale lineage reservations (#2062): a run's leftover fan-out
// reservations are swept when its lease ends, and ListTrackerDispatches
// shows the ones a run still holds.

type releaseCall struct{ username, runID string }

// recordingReservationReleaser records ReleaseRunReservations calls and,
// when revocations is set, whether the run's platform JWT was already
// revoked at the moment of the sweep.
type recordingReservationReleaser struct {
	mu          sync.Mutex
	calls       []releaseCall
	revocations *fakeRevocationStore
	revokedYet  []bool
	err         error
}

func (r *recordingReservationReleaser) ReleaseRunReservations(ctx context.Context, username, runID string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, releaseCall{username, runID})
	if r.revocations != nil {
		revoked, _ := r.revocations.IsRevoked(ctx, "jti-platform")
		r.revokedYet = append(r.revokedYet, revoked)
	}
	return 1, r.err
}

func TestEndRunLease_ReleasesTheRunsLineageReservations(t *testing.T) {
	store := newFakeRevocationStore()
	rel := &recordingReservationReleaser{revocations: store}
	s := &AgentSkillServer{}
	s.SetRevocationStore(store)
	s.SetLineageReservations(rel)

	ctx, cancel := context.WithCancel(kmsKeyTestCtx("alice", "member", "agents:run"))
	cancel() // the caller is gone before the lease ends; the sweep still runs

	s.endRunLease(ctx, testLease("run-resv"), &fakeSeedWiper{}, runExitReason)

	if len(rel.calls) != 1 || rel.calls[0] != (releaseCall{"alice", "run-resv"}) {
		t.Fatalf("ReleaseRunReservations calls = %+v, want one for (alice, run-resv)", rel.calls)
	}
	if !rel.revokedYet[0] {
		t.Error("reservations were swept before the run's credentials were revoked — the run could still start a create")
	}
}

// With no authenticated subject there is no tenant to scope the sweep to:
// the reservations stay (over-counting is the fail-closed side).
func TestEndRunLease_NoSubjectLeavesReservations(t *testing.T) {
	rel := &recordingReservationReleaser{}
	s := &AgentSkillServer{}
	s.SetLineageReservations(rel)
	s.endRunLease(context.Background(), testLease("run-anon"), &fakeSeedWiper{}, runExitReason)
	if len(rel.calls) != 0 {
		t.Errorf("ReleaseRunReservations calls = %+v, want none without a subject", rel.calls)
	}
}

// A failed sweep is logged, never fatal to the rest of the lease end.
func TestEndRunLease_ReleaseFailureStillEndsTheLease(t *testing.T) {
	store := newFakeRevocationStore()
	rel := &recordingReservationReleaser{err: errors.New("db down")}
	s := &AgentSkillServer{}
	s.SetRevocationStore(store)
	s.SetLineageReservations(rel)
	w := &fakeSeedWiper{}
	s.endRunLease(kmsKeyTestCtx("alice", "member", "agents:run"), testLease("run-resv-fail"), w, runExitReason)
	if revoked, _ := store.IsRevoked(context.Background(), "jti-platform"); !revoked {
		t.Error("platform JWT not revoked")
	}
	if w.calls() != 2 {
		t.Errorf("exec calls = %d, want 2 (wipe, remove dirs)", w.calls())
	}
}

// End to end against Postgres: a stale reservation shows on the run's
// dispatch row, its lease ends, and the row shows none — while another
// run's reservation on the same connection is untouched.
func TestListTrackerDispatches_ShowsReservationsUntilTheLeaseEnds(t *testing.T) {
	const user = "tracker-dispatch-rpc-resv"
	store := mustTestTrackerStore(t)
	ctx := context.Background()
	_ = store.Delete(ctx, user, "default") // cascades its dispatch rows
	pool, err := pgxpool.New(ctx, os.Getenv("CONTAINARIUM_TEST_DSN"))
	if err != nil {
		t.Fatalf("connect Postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	for _, q := range []string{
		"DELETE FROM tracker_issue_lineage WHERE username = $1",
		"DELETE FROM tracker_lineage_reservations WHERE username = $1",
	} {
		if _, err := pool.Exec(ctx, q, user); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
	if _, err := store.Set(ctx, tracker.Connection{
		Username: user, Name: "default", Provider: pb.TrackerProvider_TRACKER_PROVIDER_GITHUB,
		Project: "acme/widgets", CredentialSecret: "GH_TOKEN",
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	runs := map[int64]string{} // issue -> run id
	for _, issue := range []int64{11, 12} {
		d, err := store.InsertDispatch(ctx, tracker.Dispatch{Username: user, Connection: "default", IssueNumber: issue, Scope: "product", SkillID: "product-define",
			RunID: fmt.Sprintf("run-resv-%d", issue)})
		if err != nil {
			t.Fatalf("InsertDispatch(#%d): %v", issue, err)
		}
		runs[issue] = d.RunID
	}

	// Leave one stale reservation per run, the way production does: the
	// upstream create succeeds and the lineage row cannot be recorded.
	for i, issue := range []int64{11, 12} {
