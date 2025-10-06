//go:build integration

// Real-Postgres coverage for verification after a retention purge (#2878): a
// purge removes a prefix of the hash chain and moves the chain base, and
// `audit verify` must start from that base without losing the ability to
// detect tampering of what is left.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/audit/
package audit

import (
	"context"
	"strings"
	"testing"
)

// simulatePurge removes the first n rows and moves the base to the last of
// them in one transaction, the way the retention sweeper does. It returns the
// base it wrote.
func simulatePurge(t *testing.T, s *Store, n int) ChainBase {
	t.Helper()
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id int64
	var hash string
	if err := tx.QueryRow(ctx,
		`SELECT id, row_hash FROM audit_logs ORDER BY id ASC OFFSET $1 LIMIT 1`, n-1).Scan(&id, &hash); err != nil {
		t.Fatalf("pick base row: %v", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM audit_logs WHERE id <= $1`, id); err != nil {
		t.Fatalf("delete prefix: %v", err)
	}
	if err := WriteChainBase(ctx, tx, id, hash, int64(n)); err != nil {
		t.Fatalf("WriteChainBase: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
