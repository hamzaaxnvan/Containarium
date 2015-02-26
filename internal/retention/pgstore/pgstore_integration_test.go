//go:build integration

// Postgres coverage for the retention store (#2875). The in-memory store runs
// the same contract in the unit suite; this file adds what only a database
// shows: the policy and holds survive a restart (a second bootstrap over the
// same tables), and concurrent sets get distinct revisions.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/retention/pgstore/
package pgstore

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/footprintai/containarium/internal/retention"
	"github.com/footprintai/containarium/internal/retention/storetest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func retentionPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("CONTAINARIUM_TEST_DSN")
	if dsn == "" {
		t.Fatal("CONTAINARIUM_TEST_DSN is unset. Failing rather than skipping, so a lane that " +
			"loses its database reports it instead of going quietly green.")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), `DROP TABLE IF EXISTS retention_policy, retention_windows, retention_legal_holds, retention_mode, retention_upgrade_events`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	return pool
}

func pgStore(t *testing.T, pool *pgxpool.Pool) *Store {
	t.Helper()
	s, err := New(context.Background(), pool)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestRetentionPGStore_Contract(t *testing.T) {
	storetest.RunStoreContract(t, func(t *testing.T) retention.Store { return pgStore(t, retentionPool(t)) })
}

func TestRetentionPGStore_SurvivesRestart(t *testing.T) {
