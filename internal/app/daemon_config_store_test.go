package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Postgres coverage for the daemon config store (#1300).
//
// This store exists so the daemon can self-bootstrap after its VM is
// recreated: it is where the alert webhook secret and the metrics-export
// config live between restarts. A bug here is not visible at write time —
// the daemon comes back up and is quietly missing the configuration it was
// running with, which is the failure mode #1080 already burned this project
// on once.
//
// It had no test of any kind. Gated on the DSN like the rest of this
// package, not skipped unconditionally: the store-integration lane sets it
// and asserts these ran.

func newDaemonConfigStoreForTest(t *testing.T) (*DaemonConfigStore, string) {
	t.Helper()
	store, err := NewDaemonConfigStore(context.Background(), routeTestPool(t))
	if err != nil {
		t.Fatalf("NewDaemonConfigStore: %v", err)
	}
	// Unique per test so repeated or parallel runs cannot collide on the
	// primary key, and so cleanup can be scoped to this test's rows.
	prefix := fmt.Sprintf("test.%s.%d.", strings.ReplaceAll(t.Name(), "/", "_"), os.Getpid())
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(),
			"DELETE FROM daemon_config WHERE key LIKE $1", prefix+"%")
	})
	return store, prefix
}

// Set has to be an upsert. The daemon writes the same keys on every start,
// so a plain INSERT would fail on the primary key the second time the daemon
// ever booted — and the value it failed to write is the one it needs after
// the NEXT restart.
func TestDaemonConfigStore_SetIsAnUpsert(t *testing.T) {
	ctx := context.Background()
	store, prefix := newDaemonConfigStoreForTest(t)
	key := prefix + "webhook_secret"

	if err := store.Set(ctx, key, "first"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Set(ctx, key, "second"); err != nil {
		t.Fatalf("re-Set the same key: %v — a non-upsert fails on the primary key, and the "+
			"daemon rewrites these on every start", err)
	}

	got, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "second" {
		t.Errorf("value = %q, want the updated %q", got, "second")
	}
}

// A missing key returns an ERROR, not ("", nil).
//
// The doc comment on Get says "Returns empty string if not found", and that
// is wrong — it returns pgx.ErrNoRows. Worth pinning rather than quietly
// fixing the comment, because the difference decides how a caller is written:
// the one production reader (metrics_export_server.go) does
// `if raw, err := Get(...); err == nil && raw != ""`, which is correct against
// the real behaviour and would silently swallow genuine database errors if it
// had been written against the comment instead.
func TestDaemonConfigStore_GetReportsAMissingKeyAsAnError(t *testing.T) {
	ctx := context.Background()
	store, prefix := newDaemonConfigStoreForTest(t)

	got, err := store.Get(ctx, prefix+"definitely-absent")
	if err == nil {
		t.Fatalf("Get on an absent key returned (%q, nil) — a caller cannot then tell 'never "+
			"configured' from 'configured as the empty string' from 'the database is down', and "+
			"the daemon would boot with defaults believing that was the stored config", got)
	}
	if !errors.Is(err, ErrDaemonConfigNotFound) {
		t.Errorf("err = %v, want ErrDaemonConfigNotFound — matching on the message instead is "+
			"what the sentinel exists to avoid", err)
	}
	// The underlying pgx error stays reachable, so existing callers that
	// match on it are not broken by the sentinel.
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("err = %v, want pgx.ErrNoRows to remain matchable underneath", err)
	}
