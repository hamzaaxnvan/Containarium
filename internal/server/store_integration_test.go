//go:build integration

// Integration coverage for the Postgres store implementations (#1300, #1322).
//
// Everything else in this package exercises the in-memory impls, which is the
// half that cannot fail for the reasons the Postgres half exists. These cases
// run the SAME assertions against both, so the contract is stated once rather
// than restated per backend — and so a divergence between them is a failure
// rather than a discovery.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/server/
//
// The DSN is required rather than defaulted: a test that silently skips is
// indistinguishable from one that passed, which is the failure this file
// exists to stop being possible.
package server

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("CONTAINARIUM_TEST_DSN")
	if dsn == "" {
		t.Fatal("CONTAINARIUM_TEST_DSN is unset. This build tag exists to run against a real " +
			"database; failing rather than skipping, so a lane that loses its service container " +
			"reports it instead of going quietly green.")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// freshSchema drops and recreates the tables so each test starts clean and
// the migrations in the constructors are exercised every run.
func freshSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`DROP TABLE IF EXISTS agent_task_results`,
		`DROP TABLE IF EXISTS agent_tasks`,
		`DROP TABLE IF EXISTS crew_runs`,
		`DROP SEQUENCE IF EXISTS agent_lease_token_seq`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("reset schema (%s): %v", q, err)
		}
	}
}

// --- crew run store, both implementations ----------------------------

func crewStores(t *testing.T) map[string]CrewRunStore {
	t.Helper()
	pool := testPool(t)
	freshSchema(t, pool)
	pg, err := NewPostgresCrewRunStore(context.Background(), pool)
	if err != nil {
