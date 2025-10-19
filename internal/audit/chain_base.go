package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Retention of the hash-chained audit_logs (#2878, slice D of #2576; also
// #2420).
//
// audit_logs is one global chain. A row in the middle cannot be removed
// without breaking verification of every later row, and the oldest rows cannot
// be removed without a way to start verification somewhere other than genesis.
// A retention purge therefore removes a PREFIX only, and records where the
// retained chain starts in audit_chain_base, in the same transaction as the
// delete.
//
// The base is (id, hash) of the LAST purged row. The first retained row's
// prev_hash is that hash, so verification starts from it exactly as it starts
// from HashEmpty on an unpurged chain. The base proves where verification
// starts; it does not prove why the rows before it are gone. Each purge writes
// a retention.sweep audit entry (itself in the chain, and anchored by the next
// anchor), which is the record that a purge was an authorised one.

// chainBaseSchema is run by Store.initSchema. A singleton row: only the latest
// purge matters, earlier bases are history in the audit log.
const chainBaseSchema = `
	CREATE TABLE IF NOT EXISTS audit_chain_base (
		id          SMALLINT PRIMARY KEY CHECK (id = 1),
		base_id     BIGINT NOT NULL,
		base_hash   TEXT NOT NULL,
		purged_at   TIMESTAMPTZ NOT NULL,
		rows_purged BIGINT NOT NULL CHECK (rows_purged >= 0)
	);
`

// ChainBase is where verification of the retained chain starts after a purge.
type ChainBase struct {
	// ID is the id of the last purged row; every retained row has a higher id.
	ID int64
	// Hash is that row's row_hash, the prev_hash of the first retained row.
	// It is empty when every purged row predates the chain.
	Hash string
	// PurgedAt is when the latest purge moved the base.
	PurgedAt time.Time
	// RowsPurged is the total number of rows ever purged.
	RowsPurged int64
}

// ChainBaseReader is implemented by a chain that can have been purged. The
// anchor check type-asserts for it so a chain that cannot be purged (a test
// fake, an older store) behaves exactly as before.
type ChainBaseReader interface {
	ChainBase(ctx context.Context) (ChainBase, bool, error)
}

