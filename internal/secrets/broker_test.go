package secrets

import (
	"context"
	"errors"
	"os"
	"testing"

	corecrypto "github.com/footprintai/containarium/pkg/core/secrets"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration-style tests against a real Postgres, same convention as
// TestSecretsStore_Roundtrip (t.Skip in CI-without-DSN, runnable locally
// with CONTAINARIUM_TEST_DSN set — see store_test.go's docker run
// command). Exercises the broker-only delivery mode end to end: the
// SQL-level exclusion in LoadAllForUserWithDelivery, Get's write-only
// gate, BrokerCredential's read path, the one-way mode-change guard, and
// that a KEK rewrap still reaches broker rows.

func newBrokerTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("CONTAINARIUM_TEST_DSN")
	if dsn == "" {
		t.Skip("set CONTAINARIUM_TEST_DSN to run this against Postgres (the store-integration lane does)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect Postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	key := make([]byte, corecrypto.MasterKeySize)
	for i := range key {
		key[i] = byte(i + 7)
	}
	cph, err := corecrypto.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store, err := NewStore(ctx, pool, cph)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, ctx
}
