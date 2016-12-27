package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres coverage for the route store (#1300).
//
// Routes are the data plane: each record maps a public domain to a container
// IP and port, and the gateway serves traffic from them. A bug here does not
// corrupt a record an operator later notices — it sends live traffic to the
// wrong place, or nowhere.
//
// The store had no test of any kind. It is also rewritten during migration
// (#1203 updates every route's target_ip after a container moves hosts), so
// the upsert-by-domain semantics below are load-bearing for a flow that
// already ships.
//
// Gated on the DSN like the rest of this package, not skipped unconditionally:
// the store-integration lane sets it and asserts these ran.

func routeTestPool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

// newRouteStoreForTest builds a store and removes anything this test creates,
// so runs do not interfere with each other on a shared database.
func newRouteStoreForTest(t *testing.T) (*RouteStore, string) {
	t.Helper()
	ctx := context.Background()
	store, err := NewRouteStore(ctx, routeTestPool(t))
	if err != nil {
		t.Fatalf("NewRouteStore: %v", err)
	}
	// Unique per test so parallel or repeated runs cannot collide on the
	// full_domain UNIQUE constraint.
	suffix := fmt.Sprintf("%s-%d", t.Name(), os.Getpid())
	return store, suffix
}

func TestRouteStore_SaveIsAnUpsertByDomain(t *testing.T) {
	ctx := context.Background()
	store, suffix := newRouteStoreForTest(t)
	domain := "app-" + suffix + ".example.com"
	t.Cleanup(func() { _ = store.Delete(context.Background(), domain) })

	first := &RouteRecord{
		Subdomain: "app-" + suffix, FullDomain: domain,
		TargetIP: "10.0.0.10", TargetPort: 8080, Protocol: "http",
		ContainerName: "alice-container", Active: true,
	}
	if err := store.Save(ctx, first); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The same domain again must UPDATE, not insert a second row — full_domain
	// is UNIQUE, so a non-upsert would error, and a caller re-registering a
	// route after a restart would fail rather than refresh.
	second := *first
	second.TargetIP = "10.0.0.99"
	second.TargetPort = 9090
	if err := store.Save(ctx, &second); err != nil {
		t.Fatalf("re-Save the same domain: %v", err)
	}

	got, err := store.GetByDomain(ctx, domain)
	if err != nil {
		t.Fatalf("GetByDomain: %v", err)
	}
	if got == nil {
		t.Fatal("the route disappeared after being re-saved")
	}
	if got.TargetIP != "10.0.0.99" || got.TargetPort != 9090 {
		t.Errorf("route points at %s:%d, want the updated 10.0.0.99:9090 — this is the write "+
