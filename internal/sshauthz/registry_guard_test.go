package sshauthz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A store with no HolderLookup must not revoke: skipping the holder check
// silently would bring back #3222 at any construction site that forgot it.
func TestPGKeyStore_RevokeWithoutHolderLookupErrors(t *testing.T) {
	var s PGKeyStore // zero value: no pool, no holders; must fail before touching the pool
	pub := testKey(t)
	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	rec, err := NewKeyRecord(pub, "alice", SourceAPI, at)
	if err != nil {
		t.Fatal(err)
