//go:build integration

// Real-Postgres coverage for sealing a Proof Pack into the audit hash chain
// and verifying it (#2798), including after a retention purge of the chain's
// prefix.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/proofpack/
package proofpack

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/testsupport/postgres"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestMain(m *testing.M) {
	os.Exit(postgres.Run(m.Run))
}

func sealTestStore(t *testing.T) (*audit.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("CONTAINARIUM_TEST_DSN")
	if dsn == "" {
		t.Fatal("CONTAINARIUM_TEST_DSN is unset; failing rather than skipping")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS audit_logs, audit_chain_base`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	s, err := audit.NewStore(ctx, pool)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, pool
}

func logRows(t *testing.T, s *audit.Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.Log(context.Background(), &audit.AuditEntry{Username: "tenant", Action: "container_create", ResourceType: "container", StatusCode: 200}); err != nil {
			t.Fatalf("log: %v", err)
		}
	}
}

// sealOnRealChain writes 5 rows, the seal, then 5 more, and returns the
// sealed pack (its seal row is id 6).
func sealOnRealChain(t *testing.T, s *audit.Store) *pb.SealedProofPack {
	t.Helper()
	logRows(t, s, 5)
	sealed, err := Seal(context.Background(), s, "tenant", "run-1", samplePack())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	logRows(t, s, 5)
	if sealed.GetSeal().GetAuditRowId() != 6 {
		t.Fatalf("seal row id = %d, want 6", sealed.GetSeal().GetAuditRowId())
	}
	return sealed
}

// purgePrefix removes rows up to and including id and moves the chain base
// there in one transaction, the way the retention sweeper does.
func purgePrefix(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var hash string
	if err := tx.QueryRow(ctx, `SELECT row_hash FROM audit_logs WHERE id = $1`, id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM audit_logs WHERE id <= $1`, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.WriteChainBase(ctx, tx, id, hash, tag.RowsAffected()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSealIntegration_VerifyOnRealChain(t *testing.T) {
	s, _ := sealTestStore(t)
	sealed := sealOnRealChain(t, s)
	res, err := Verify(context.Background(), s, sealed)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.AuditRowID != 6 || res.ChainCheckedThrough != 11 {
		t.Errorf("result = %+v, want seal row 6 checked through tip 11", res)
	}
	// #3064: the tip hash is the real row_hash of the last row.
	if tip, ok, err := s.ChainEntryAt(context.Background(), 11); err != nil || !ok || tip.RowHash == "" || res.ChainTipHash != tip.RowHash {
		t.Errorf("ChainTipHash = %q, want row 11's row_hash %q (%v)", res.ChainTipHash, tip.RowHash, err)
	}
	if first, err := s.VerifyChainSinceID(context.Background(), 0, 0); err != nil || first != 0 {
		t.Errorf("whole chain after sealing: first bad %d, err %v", first, err)
	}
}

// TestSealIntegration_VerifyAfterSignerRotation (#2729): a pack sealed before
// a guardrail signing key is rotated and then revoked still verifies after
