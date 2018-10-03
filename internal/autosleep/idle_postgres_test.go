package autosleep

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/footprintai/containarium/internal/traffic"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// LastNetworkActivity against real Postgres (#1300, guarding #524).
//
// The existing test in idle_test.go asserts that the SQL STRING contains
// "ended_at IS NULL" and "now()". That is a grep over query text, not a check
// of what the query returns: it would pass for any query containing those
// substrings, however the semantics came out. For the guard on #524 — "a box
// running an active session is NOT stopped" — that is not enough, because the
// bug being guarded against was a wrong ANSWER from a query that already
// mentioned the right columns.
//
// This runs the real query against real rows. What it pins:
//
//   - An OPEN connection (ended_at IS NULL) reports activity AS OF NOW, so a
//     long-lived SSH or exec session keeps the box awake for as long as
//     someone is connected. The pre-#524 COALESCE(ended_at, started_at) pinned
//     it to started_at, so a session open LONGER than the idle threshold looked
//     idle and the box was slept out from under the person debugging it.
//   - A CLOSED connection contributes its ended_at, its true last-activity
//     instant.
//   - No rows reports the zero time, which Decide treats as "no traffic ever
//     recorded" rather than as epoch 0.

func idleTestPool(t *testing.T) (*pgxpool.Pool, *traffic.Store, string) {
	t.Helper()
	dsn := os.Getenv("CONTAINARIUM_TEST_DSN")
	if dsn == "" {
		t.Skip("set CONTAINARIUM_TEST_DSN to run this against Postgres (the store-integration lane does)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	// The traffic store owns the schema this query reads, so let it create the
	// table rather than duplicating the DDL here — a copy would drift and this
	// test would then pass against a table the daemon does not use.
	store, err := traffic.NewStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("traffic.NewStore: %v", err)
	}
	t.Cleanup(store.Close)

	container := fmt.Sprintf("t%d-%s", os.Getpid(), t.Name())
	if len(container) > 60 {
		container = container[:60]
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			"DELETE FROM traffic_connections WHERE container_name = $1", container)
	})
	return pool, store, container
}

// saveConn writes one connection. A nil lastSeen leaves ended_at NULL, which
// is how the collector represents a connection that is still open.
func saveConn(t *testing.T, store *traffic.Store, container, id string, firstSeen time.Time, lastSeen *time.Time) {
	t.Helper()
	c := &pb.Connection{
		Id:            id,
		ContainerName: container,
		Protocol:      pb.Protocol_PROTOCOL_TCP,
		SourceIp:      "10.0.0.10",
		SourcePort:    54321,
		DestIp:        "93.184.216.34",
		DestPort:      443,
		Direction:     pb.TrafficDirection_TRAFFIC_DIRECTION_EGRESS,
		FirstSeen:     timestamppb.New(firstSeen),
	}
	if lastSeen != nil {
		c.LastSeen = timestamppb.New(*lastSeen)
	}
	if err := store.SaveConnection(context.Background(), c); err != nil {
		t.Fatalf("SaveConnection(%s): %v", id, err)
	}
}

// THE #524 case: a session opened long ago and still open must report activity
// as of NOW, not as of when it started.
func TestIntegrationIdle_OpenConnectionReportsActivityAsOfNow(t *testing.T) {
	ctx := context.Background()
	pool, store, container := idleTestPool(t)

	// Opened two hours ago and never closed — a debug session someone is
	// sitting in right now.
