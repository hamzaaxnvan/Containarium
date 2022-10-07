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
		t.Fatalf("NewPostgresCrewRunStore: %v", err)
	}
	return map[string]CrewRunStore{"mem": NewMemCrewRunStore(), "postgres": pg}
}

func TestCrewRunStore_ContractHoldsForBothImpls(t *testing.T) {
	ctx := context.Background()
	for name, s := range crewStores(t) {
		t.Run(name, func(t *testing.T) {
			run := &pb.CrewRun{Id: "r1", CrewId: "crew-1", State: pb.CrewRunState_CREW_RUN_STATE_RUNNING}
			if err := s.Put(ctx, run); err != nil {
				t.Fatalf("put: %v", err)
			}

			got, ok, err := s.Get(ctx, "r1")
			if err != nil || !ok {
				t.Fatalf("get: ok=%v err=%v", ok, err)
			}
			if got.State != pb.CrewRunState_CREW_RUN_STATE_RUNNING || got.CrewId != "crew-1" {
				t.Errorf("round trip lost fields: %+v", got)
			}

			// Mutating what you put must not change what is stored.
			run.State = pb.CrewRunState_CREW_RUN_STATE_FAILED
			again, _, _ := s.Get(ctx, "r1")
			if again.State != pb.CrewRunState_CREW_RUN_STATE_RUNNING {
				t.Error("the caller's later writes reached the store")
			}

			if _, ok, _ := s.Get(ctx, "missing"); ok {
				t.Error("a missing run reported found")
			}
		})
	}
}

// #1182 AC4 against a real database: the sweep has to update both the state
// column and the state inside the marshalled proto. A row disagreeing with
// itself reports differently depending on which one a reader trusts, and only
// Postgres can show that.
func TestCrewRunStore_FailStrandedForBothImpls(t *testing.T) {
	ctx := context.Background()
	for name, s := range crewStores(t) {
		t.Run(name, func(t *testing.T) {
			mustPutI(t, s, &pb.CrewRun{Id: "stuck", State: pb.CrewRunState_CREW_RUN_STATE_RUNNING})
			mustPutI(t, s, &pb.CrewRun{Id: "done", State: pb.CrewRunState_CREW_RUN_STATE_COMPLETED, ArtifactJson: "{}"})

			n, err := s.FailStranded(ctx, "", StrandedByRestart)
			if err != nil {
				t.Fatalf("FailStranded: %v", err)
			}
			if n != 1 {
				t.Errorf("reconciled %d, want 1", n)
			}

			stuck, _, _ := s.Get(ctx, "stuck")
			if stuck.State != pb.CrewRunState_CREW_RUN_STATE_FAILED {
				t.Errorf("state = %v, want FAILED", stuck.State)
			}
			if stuck.Error != StrandedByRestart {
				t.Errorf("error = %q, want the restart reason — this is the field that comes back "+
					"from the marshalled proto, so it proves both copies were updated", stuck.Error)
			}

			done, _, _ := s.Get(ctx, "done")
			if done.State != pb.CrewRunState_CREW_RUN_STATE_COMPLETED || done.ArtifactJson != "{}" {
				t.Errorf("a completed run was altered: %+v", done)
			}

			if again, _ := s.FailStranded(ctx, "", StrandedByRestart); again != 0 {
				t.Errorf("second sweep reconciled %d, want 0 (idempotent)", again)
