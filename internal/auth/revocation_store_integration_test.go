//go:build integration

// Integration coverage for the JWT revocation store (#1300).
//
// This is a security control with a simple failure mode: if a revoked token
// does not read as revoked, it stays valid until it expires on its own. The
// SQL had no test — the store is one of the pgxpool users this repo could not
// exercise until the store-integration lane existed.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/auth/
package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func revocationStore(t *testing.T) *PgRevocationStore {
	t.Helper()
	dsn := os.Getenv("CONTAINARIUM_TEST_DSN")
	if dsn == "" {
		t.Fatal("CONTAINARIUM_TEST_DSN is unset. Failing rather than skipping — a skipped test " +
			"and a passing one look identical, which is how this store went untested.")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), `DROP TABLE IF EXISTS jwt_revocations`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	s, err := NewPgRevocationStore(context.Background(), pool)
	if err != nil {
		t.Fatalf("NewPgRevocationStore: %v", err)
	}
	return s
}

// The whole point of the store: a revoked token reads as revoked, and an
// unrelated one does not.
func TestRevocationStore_RevokedTokenReadsAsRevoked(t *testing.T) {
	ctx := context.Background()
	s := revocationStore(t)

	if err := s.Revoke(ctx, "jti-1", time.Now().Add(time.Hour), "operator_revoke"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	revoked, err := s.IsRevoked(ctx, "jti-1")
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if !revoked {
		t.Error("a revoked token did not read as revoked — it would stay valid until it expired " +
			"on its own, which is the entire failure this store exists to prevent")
	}

	other, err := s.IsRevoked(ctx, "jti-2")
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
