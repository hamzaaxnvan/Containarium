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
	return s
}

// The contract MemOwnerRevocations states, held by the durable store: an
// unrevoked or empty owner reads as the zero time, a revocation records its
// cutoff for that owner only, the latest cutoff wins, and an earlier one never
// narrows it.
func TestOwnerRevocationStore_CutoffContract(t *testing.T) {
	ctx := context.Background()
	s := ownerRevocationStore(t)
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	for _, owner := range []string{"", "org:never-revoked"} {
		got, err := s.RevokedBefore(ctx, owner)
		if err != nil {
			t.Fatalf("RevokedBefore(%q): %v", owner, err)
		}
		if !got.IsZero() {
			t.Errorf("RevokedBefore(%q) = %v, want the zero time", owner, got)
		}
	}

	steps := []struct {
		name string
		at   time.Time
		want time.Time
	}{
		{"first revocation records its cutoff", t0, t0},
		{"a later revocation widens it", t0.Add(time.Hour), t0.Add(time.Hour)},
		{"an earlier revocation must never narrow it", t0.Add(-time.Hour), t0.Add(time.Hour)},
		{"the same cutoff again is a no-op", t0.Add(time.Hour), t0.Add(time.Hour)},
	}
	for _, st := range steps {
		if err := s.RevokeOwner(ctx, "org:a", st.at, "provider key deleted"); err != nil {
			t.Fatalf("%s: RevokeOwner: %v", st.name, err)
		}
		got, err := s.RevokedBefore(ctx, "org:a")
		if err != nil {
			t.Fatalf("%s: RevokedBefore: %v", st.name, err)
		}
		if !got.Equal(st.want) {
			t.Errorf("%s: cutoff = %v, want %v", st.name, got, st.want)
		}
	}

	other, err := s.RevokedBefore(ctx, "org:b")
	if err != nil {
		t.Fatalf("RevokedBefore(org:b): %v", err)
	}
