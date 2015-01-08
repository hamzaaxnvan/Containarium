//go:build integration

// Real-Postgres coverage for enforcement (#2877): rows are seeded on both
// sides of a cutoff, the whole path the daemon runs (Postgres store, Postgres
// sources, sweeper) is driven in each mode, and the rows left in the tables
// are what must match.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/retention/pgstore/
package pgstore_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/footprintai/containarium/internal/retention"
	"github.com/footprintai/containarium/internal/retention/pgstore"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// auditCounter stands in for the audit store and counts the entries an
// enforced sweep writes.
type auditCounter struct{ n int }

func (a *auditCounter) audit(context.Context, *pb.RetentionReport) error { a.n++; return nil }

// enforcePath is the daemon's retention path over a seeded database, with the
// three classes the swept tables use set to the test window.
func enforcePath(t *testing.T) (context.Context, *pgxpool.Pool, *pgstore.Store, *retention.Sweeper, *auditCounter) {
	t.Helper()
	ctx := context.Background()
	pool := sourcesPool(t)
	seed(t, pool)
	store, err := pgstore.New(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPolicy(ctx, []*pb.RetentionWindow{
		{Class: pb.RetentionClass_RETENTION_CLASS_PROMPTS, Days: sourceWindowDays},
		{Class: pb.RetentionClass_RETENTION_CLASS_RESPONSES, Days: sourceWindowDays},
		{Class: pb.RetentionClass_RETENTION_CLASS_GATEWAY_LOGS, Days: sourceWindowDays},
	}, "admin", nil); err != nil {
		t.Fatal(err)
	}
	a := &auditCounter{}
	sw := retention.NewSweeper(store, pgstore.NewSources(pool), nil)
	sw.SetAuditor(a.audit)
	return ctx, pool, store, sw, a
}

func setMode(t *testing.T, ctx context.Context, s *pgstore.Store, m pb.RetentionMode) {
	t.Helper()
	if _, err := s.SetMode(ctx, m, "admin", nil); err != nil {
		t.Fatalf("SetMode %v: %v", m, err)
	}
}

func wantRows(t *testing.T, pool *pgxpool.Pool, want int, why string) {
	t.Helper()
	for tbl, n := range rowCounts(t, pool) {
		if n != want {
			t.Errorf("%s has %d rows, want %d (%s)", tbl, n, want, why)
		}
	}
}

func TestEnforce_RemovesOlderRecordsKeepsNewerAndDryRunRemovesNothing(t *testing.T) {
	ctx, pool, store, sw, audits := enforcePath(t)

	// DRY_RUN is the default: a full sweep removes nothing.
	rep, err := sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("dry-run Sweep: %v", err)
	}
	if rep.GetEnforced() || rep.GetMode() != pb.RetentionMode_RETENTION_MODE_DRY_RUN {
		t.Errorf("default report = mode %v enforced %v, want DRY_RUN and not enforced", rep.GetMode(), rep.GetEnforced())
	}
	wantRows(t, pool, 3, "DRY_RUN must remove nothing")
	if audits.n != 0 {
		t.Errorf("a DRY_RUN sweep wrote %d audit entries, want none", audits.n)
	}

	setMode(t, ctx, store, pb.RetentionMode_RETENTION_MODE_ENFORCE)
	rep, err = sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("enforced Sweep: %v", err)
	}
	if !rep.GetEnforced() {
		t.Error("report of an ENFORCE sweep is not marked enforced")
	}
	wantRows(t, pool, 1, "the two rows past the window are gone, the newer one is kept")
	if audits.n != 1 {
		t.Errorf("the enforced sweep wrote %d audit entries, want exactly one", audits.n)
	}
	if _, err := sw.Sweep(ctx); err != nil { // nothing is left to remove
		t.Fatalf("second enforced Sweep: %v", err)
	}
	if audits.n != 1 {
		t.Errorf("a sweep with nothing eligible wrote an audit entry (%d in all), want none", audits.n)
	}
	var kept int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_tasks WHERE id = 't-c'`).Scan(&kept); err != nil || kept != 1 {
		t.Errorf("the newer agent_tasks row t-c: count %d, err %v; want it kept", kept, err)
	}
	var removed int64
	for _, s := range rep.GetStores() {
		removed += s.GetRemoved()
	}
	if removed != 10 { // 2 each in 5 tables; crew_runs is removed once, not once per class
		t.Errorf("report removed %d records in all, want 10 (crew_runs once)", removed)
	}
}

func TestEnforce_AHeldOrgsRecordsSurviveAndGoOnTheNextSweepAfterRelease(t *testing.T) {
	ctx, pool, store, sw, _ := enforcePath(t)
	setMode(t, ctx, store, pb.RetentionMode_RETENTION_MODE_ENFORCE)
	if _, err := store.SetHold(ctx, "org-a", "case 1", "admin", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := sw.Sweep(ctx); err != nil {
		t.Fatalf("held Sweep: %v", err)
	}
	wantRows(t, pool, 3, "while a hold is active no org-less record is removed (Q21 on #2727)")

	if _, err := store.ReleaseHold(ctx, "org-a", "admin", nil); err != nil {
		t.Fatal(err)
	}
