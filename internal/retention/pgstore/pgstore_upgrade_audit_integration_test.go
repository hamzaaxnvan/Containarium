//go:build integration

// Q31 on #2727: an upgrade change to stored retention data (a clamped
// window, a normalised, merged or quarantined hold) is recorded in the same
// transaction as the change and audited once an audit store exists; a clamp
// bumps the policy revision.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/footprintai/containarium/internal/retention"
)

// pendingActions lists the recorded upgrade changes not yet audited.
func pendingActions(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), `SELECT action FROM retention_upgrade_events WHERE audited_at IS NULL ORDER BY id`)
	if err != nil {
		t.Fatalf("read upgrade events: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

// drain audits every pending event into a slice.
func drain(t *testing.T, s *Store) []retention.UpgradeEvent {
	t.Helper()
	var got []retention.UpgradeEvent
	if _, err := s.AuditUpgradeEvents(context.Background(), func(ev retention.UpgradeEvent) error {
		got = append(got, ev)
		return nil
	}); err != nil {
		t.Fatalf("AuditUpgradeEvents: %v", err)
	}
	return got
}

func TestRetentionPGStore_AClampIsRecordedAuditableOnceAndBumpsTheRevision(t *testing.T) {
	ctx := context.Background()
	pool := retentionPool(t)
	createSchema2917(t, pool, `INSERT INTO retention_windows (class, days) VALUES (1, 2000000000), (2, 45);`)
