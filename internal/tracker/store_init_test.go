package tracker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Concurrent-init tests (#2061). Every other store test shares the DSN's
// default schema, which the first NewStore in the process already
// initialized — so none of them can exercise a genuine FIRST run. These
// tests create a uniquely named, empty schema per round and point every
// pool's search_path at it, so the tracker tables really do not exist yet
// when the inits race.

// freshSchema creates an empty schema on the test database and returns
// its name. Dropped (CASCADE) when the test ends.
func freshSchema(t *testing.T, ctx context.Context, dsn string) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("random schema suffix: %v", err)
	}
	schema := "tracker_init_" + hex.EncodeToString(b[:])
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect Postgres: %v", err)
	}
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE")
		_ = admin.Close(context.Background())
	})
	return schema
}

// newPoolInSchema opens a pool whose connections resolve unqualified
// table names in schema, so Store.initSchema creates its tables there.
// One pool per NewStore mimics one daemon replica per init: the racing
// inits never share a connection.
func newPoolInSchema(t *testing.T, ctx context.Context, dsn, schema string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect Postgres: %v", err)
	}
	t.Cleanup(pool.Close)
