//go:build integration

// Integration coverage for the durable owner-revocation store (#2111).
//
// The in-memory MemOwnerRevocations is the reference shape; this is the
// Postgres half the daemon wires, and the whole reason it exists is that a
// cutoff must outlive the process that recorded it. Each case therefore runs
// against a real database, and the restart case rebuilds the store on a fresh
// pool rather than reusing the one that wrote the row.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/auth/
package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func ownerRevocationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("CONTAINARIUM_TEST_DSN")
	if dsn == "" {
		t.Fatal("CONTAINARIUM_TEST_DSN is unset. Failing rather than skipping — a skipped test " +
			"and a passing one look identical.")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// ownerRevocationStore returns a store on a freshly reset table, so the schema
// creation in the constructor is exercised every run.
func ownerRevocationStore(t *testing.T) *PgOwnerRevocationStore {
	t.Helper()
	pool := ownerRevocationPool(t)
	if _, err := pool.Exec(context.Background(), `DROP TABLE IF EXISTS gateway_owner_revocations`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	s, err := NewPgOwnerRevocationStore(context.Background(), pool)
	if err != nil {
		t.Fatalf("NewPgOwnerRevocationStore: %v", err)
	}
