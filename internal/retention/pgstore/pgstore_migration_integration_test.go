//go:build integration

// Concurrency and upgrade coverage for the retention store (#2875 review
// follow-up on #2933): a set that waited on the policy row lock reports the
// windows committed before it, and a bootstrap over the schema #2917
// deployed clamps an over-cap window and normalises padded hold ids
// instead of failing.
package pgstore

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/footprintai/containarium/internal/retention"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// createSchema2917 creates the retention tables exactly as #2917 merged
// them (no upper bound on days, no mode table), then runs seed.
func createSchema2917(t *testing.T, pool *pgxpool.Pool, seed string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		CREATE TABLE retention_policy (
			id         SMALLINT PRIMARY KEY CHECK (id = 1),
			revision   BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ,
			updated_by TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO retention_policy (id, revision, updated_at, updated_by) VALUES (1, 1, NOW(), 'admin-a');
		CREATE TABLE retention_windows (
			class INTEGER PRIMARY KEY,
			days  INTEGER NOT NULL CHECK (days > 0)
		);
		CREATE TABLE retention_legal_holds (
			org_id TEXT PRIMARY KEY,
			reason TEXT NOT NULL,
			set_by TEXT NOT NULL,
			set_at TIMESTAMPTZ NOT NULL
		);
	`+seed); err != nil {
		t.Fatalf("create the #2917 schema: %v", err)
	}
}

func windowDays(p *pb.RetentionPolicy, c pb.RetentionClass) int32 {
	for _, w := range p.GetWindows() {
		if w.GetClass() == c {
			return w.GetDays()
		}
	}
	return 0
}

const (
	classPrompts   = pb.RetentionClass_RETENTION_CLASS_PROMPTS
	classResponses = pb.RetentionClass_RETENTION_CLASS_RESPONSES
)

// TestRetentionPGStore_ASetThatWaitedOnTheLockSeesWindowsCommittedBeforeIt:
// another transaction holds the policy row and changes a window; a SetPolicy
// that waited on that lock must report the committed window in Previous and
// Current (and so in its audit entry), not the snapshot it started with.
func TestRetentionPGStore_ASetThatWaitedOnTheLockSeesWindowsCommittedBeforeIt(t *testing.T) {
	ctx := context.Background()
	pool := retentionPool(t)
	s := pgStore(t, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM retention_policy WHERE id = 1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO retention_windows (class, days) VALUES (1, 11)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE retention_policy SET revision = revision + 1, updated_by = 'admin-a' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	type result struct {
		res *retention.PolicySetResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := s.SetPolicy(ctx, []*pb.RetentionWindow{{Class: classResponses, Days: 12}}, "admin-b", nil)
		done <- result{res, err}
	}()
	// Wait until the set is blocked on the policy row lock.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND query LIKE '%retention_policy%FOR UPDATE%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("SetPolicy never waited on the policy row lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("SetPolicy: %v", r.err)
	}
	if got := windowDays(r.res.Previous, classPrompts); got != 11 || r.res.Previous.GetRevision() != 1 {
		t.Errorf("Previous = prompts %d at revision %d, want prompts 11 at revision 1 (the committed state it waited for)", got, r.res.Previous.GetRevision())
	}
	if p, q := windowDays(r.res.Current, classPrompts), windowDays(r.res.Current, classResponses); p != 11 || q != 12 {
		t.Errorf("Current = prompts %d, responses %d; want 11 and 12 (Current dropped the window committed before it)", p, q)
	}
}

// TestRetentionPGStore_ConcurrentSetsOnDifferentClassesNeverReportStale is
// the reviewer's 40-round repro: two sets on different classes race; the
// later one's Current must hold both values.
func TestRetentionPGStore_ConcurrentSetsOnDifferentClassesNeverReportStale(t *testing.T) {
	ctx := context.Background()
	s := pgStore(t, retentionPool(t))
	stale := 0
	const rounds = 40
	for i := int32(0); i < rounds; i++ {
		a, b := 100+i, 200+i
		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make([]*retention.PolicySetResult, 2)
		for j, w := range []*pb.RetentionWindow{{Class: classPrompts, Days: a}, {Class: classResponses, Days: b}} {
