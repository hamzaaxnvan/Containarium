package backup

import (
	"os"
	"strings"
	"testing"
	"time"
)

// steppedClock returns a clock that advances by step on every call, so a
// sequence of Create calls gets distinct, ordered, deterministic
// timestamps — needed because Prune's whole job is ordering by CreatedAt.
func steppedClock(start time.Time, step time.Duration) func() time.Time {
	t := start
	return func() time.Time {
		cur := t
		t = t.Add(step)
		return cur
	}
}

// createN makes n plaintext local backups for (username, database) in
// order, oldest first, using m's current clock. Returns the created
// records in the SAME (oldest-first) order, for readable test assertions.
func createN(t *testing.T, m *Manager, username, database string, n int) []*Record {
	t.Helper()
	var out []*Record
	for i := 0; i < n; i++ {
		r, err := m.Create(CreateOptions{
			Username: username, ContainerName: username + "-container",
			Conn: PgConn{Database: database}, Destination: DestLocal,
		})
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		out = append(out, r)
	}
	return out
}

func idsOf(recs []*Record) []string {
	ids := make([]string, len(recs))
	for i, r := range recs {
		ids[i] = r.ID
	}
	return ids
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func TestPrune_KeepsNewestNPerDatabase(t *testing.T) {
	m := newTestManager(t, newFakeOps([]byte("x")))
	m.clock = steppedClock(fixedClock(), time.Second)
	recs := createN(t, m, "alice", "app", 5) // oldest..newest

	res, err := m.Prune(PruneOptions{Username: "alice", Database: "app", Keep: 2})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(res.Failures) != 0 {
		t.Fatalf("unexpected failures: %v", res.Failures)
	}
	wantDeleted := idsOf(recs[:3]) // the 3 oldest
	if len(res.Deleted) != 3 {
		t.Fatalf("deleted %v, want 3 ids (the oldest)", res.Deleted)
