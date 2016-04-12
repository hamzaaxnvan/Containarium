//go:build integration

package audit

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// #2415 — Store.LogBatch and the dedupe_key column, against a real Postgres.

func batchEntries(n, offset int) []BatchEntry {
	out := make([]BatchEntry, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("sess-%d", offset+i)
		out[i] = BatchEntry{
			Entry: AuditEntry{
				Timestamp:    time.Unix(1_700_000_000+int64(offset+i), 0),
				Username:     "alice",
				Action:       "ssh_session_open",
				ResourceType: "ssh_session",
				ResourceID:   id,
				SourceIP:     "203.0.113.7",
			},
			DedupeKey: "sshsession:" + id + ":open",
		}
	}
	return out
}

func mustVerify(t *testing.T, s *Store) {
	t.Helper()
	firstBad, err := s.VerifyChainSinceID(context.Background(), 0, 1000)
	if err != nil || firstBad != 0 {
		t.Fatalf("chain does not verify: firstBad=%d err=%v", firstBad, err)
	}
}

// rowCount counts rows rather than reading MaxRowID: ON CONFLICT DO NOTHING
// consumes a BIGSERIAL value per skipped duplicate, so ids have gaps (as they
// already do after any rolled-back insert). The chain links by prev_hash, not
// id contiguity, so gaps are harmless; the row count is what matters.
func rowCount(t *testing.T, s *Store) int64 {
	t.Helper()
	var n int64
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLogBatch_FreshEntriesInsertAndVerify(t *testing.T) {
	s := auditTestStore(t)
	outs, err := s.LogBatch(context.Background(), batchEntries(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	for i, o := range outs {
		if o != BatchInserted {
			t.Fatalf("outcome[%d] = %v, want BatchInserted", i, o)
		}
	}
	if got := rowCount(t, s); got != 10 {
		t.Fatalf("rows = %d, want 10", got)
	}
	mustVerify(t, s)
}

func TestLogBatch_ReplayIsNoOp(t *testing.T) {
	s := auditTestStore(t)
	b := batchEntries(8, 0)
	if _, err := s.LogBatch(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	outs, err := s.LogBatch(context.Background(), batchEntries(8, 0))
	if err != nil {
		t.Fatal(err)
	}
	for i, o := range outs {
		if o != BatchDuplicate {
			t.Fatalf("replay outcome[%d] = %v, want BatchDuplicate", i, o)
		}
	}
	if got := rowCount(t, s); got != 8 {
		t.Fatalf("rows after replay = %d, want 8 (unchanged)", got)
	}
	mustVerify(t, s)
}

// A duplicate must be skipped before it can become anyone's predecessor: the
// row after a duplicate chains to the last INSERTED row.
func TestLogBatch_MixedBatchKeepsChainLinear(t *testing.T) {
	s := auditTestStore(t)
	if _, err := s.LogBatch(context.Background(), batchEntries(3, 0)); err != nil { // sess-0..2
		t.Fatal(err)
	}
	// order: new(100), dup(1), dup(2), new(101)
	n := batchEntries(2, 100) // sess-100, sess-101
	d := batchEntries(2, 1)   // sess-1, sess-2 (already stored)
	mixed := []BatchEntry{n[0], d[0], d[1], n[1]}
	outs, err := s.LogBatch(context.Background(), mixed)
	if err != nil {
		t.Fatal(err)
	}
	want := []BatchOutcome{BatchInserted, BatchDuplicate, BatchDuplicate, BatchInserted}
	for i := range want {
		if outs[i] != want[i] {
			t.Fatalf("outcome[%d] = %v, want %v (all: %v)", i, outs[i], want[i], outs)
		}
