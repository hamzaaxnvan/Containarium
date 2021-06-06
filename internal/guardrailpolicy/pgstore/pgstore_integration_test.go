//go:build integration

// Postgres coverage for the guardrail policy store (#2368). The in-memory
// store runs the same contract in the unit suite; this file adds what only a
// database can show: the revision survives a restart, concurrent Sets get
// distinct revisions, and a read error, an undecodable stored policy or a
// missing singleton row is never "not configured".
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/guardrailpolicy/pgstore/
package pgstore

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	"github.com/footprintai/containarium/internal/guardrailpolicy"
	"github.com/footprintai/containarium/internal/guardrailpolicy/storetest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func policyPool(t *testing.T) *pgxpool.Pool {
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
	if _, err := pool.Exec(context.Background(), `DROP TABLE IF EXISTS guardrail_policy, guardrail_baseline`); err != nil {
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

func TestGuardrailPolicyPGStore_Contract(t *testing.T) {
	storetest.RunStoreContract(t, func(t *testing.T) guardrailpolicy.Store { return pgStore(t, policyPool(t)) })
}

// TestGuardrailPolicyPGStore_RevisionSurvivesRestart: a second store over the same
// database (a restarted daemon re-running the schema bootstrap) reads the
// stored policy and continues the revision sequence rather than restarting it.
func TestGuardrailPolicyPGStore_RevisionSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	pool := policyPool(t)
	first := pgStore(t, pool)
	for i := 0; i < 2; i++ {
		if _, err := first.Set(ctx, storetest.Req(storetest.Policy(int32(i)), storetest.Signer(t, 3, "k")), "admin-a"); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	before, err := first.Get(ctx)
