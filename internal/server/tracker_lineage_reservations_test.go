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
