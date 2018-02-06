//go:build integration

// Integration coverage for FindingStore (#1639): a finding that doesn't
// really land in the audit hash chain, or doesn't survive a daemon restart,
// is a finding an operator can't trust — the sentry's entire point is being
// the thing nothing else already is. That's why this exercises real
// Postgres, a real events.Bus, and the real audit chain rather than mocks.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/threatdetect/
package threatdetect

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/events"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func threatdetectPool(t *testing.T) *pgxpool.Pool {
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
	// Only reset security_findings: it's this package's own table. audit_logs
	// belongs to internal/audit, whose own integration tests run concurrently
	// against this same database in CI (`go test ./pkgA/ ./pkgB/ ...` runs
	// package binaries in parallel) — dropping it here raced their schema
	// setup and produced spurious "relation does not exist" / duplicate
	// pg_type failures. audit.NewStore's initSchema is idempotent and safe
	// to call against a table this package doesn't own.
	// security_actions (#2636) references security_findings, so it goes
	// first.
	if _, err := pool.Exec(context.Background(), `DROP TABLE IF EXISTS security_actions`); err != nil {
		t.Fatalf("reset (security_actions): %v", err)
	}
	if _, err := pool.Exec(context.Background(), `DROP TABLE IF EXISTS security_findings`); err != nil {
		t.Fatalf("reset (security_findings): %v", err)
	}
	return pool
}

func newStores(t *testing.T, pool *pgxpool.Pool) (*FindingStore, *audit.Store, *events.Bus) {
	t.Helper()
	ctx := context.Background()

	auditStore, err := audit.NewStore(ctx, pool)
	if err != nil {
		t.Fatalf("audit.NewStore: %v", err)
	}

	bus := events.NewBus()
	emitter := events.NewEmitter(bus)

	fs, err := NewFindingStore(ctx, pool, emitter, auditStore)
	if err != nil {
		t.Fatalf("NewFindingStore: %v", err)
	}
	return fs, auditStore, bus
}

func sampleFinding() *Finding {
	return &Finding{
		Rule:      pb.ThreatRuleId_THREAT_RULE_ID_BAD_DESTINATION,
		Severity:  pb.ThreatSeverity_THREAT_SEVERITY_HIGH,
		TenantID:  "tenant-a",
		Container: "c-miner",
		BackendID: "backend-1",
		Subject:   "203.0.113.9",
		Evidence: Evidence{
			Flows: []FlowEvidence{
				{SrcIP: "10.0.0.5", DstIP: "203.0.113.9", SrcPort: 51234, DstPort: 443, Protocol: "tcp", Bytes: 4096, Packets: 12},
			},
		},
	}
}

// The #1639 acceptance test: every finding lands in the audit log inside the
// existing hash chain, and VerifyChainSinceID still passes over a window
// containing findings.
func TestFindingStore_InsertRidesTheAuditChain(t *testing.T) {
	ctx := context.Background()
	pool := threatdetectPool(t)
	fs, auditStore, _ := newStores(t, pool)

	beforeID, err := auditStore.MaxRowID(ctx)
