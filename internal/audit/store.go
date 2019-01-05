package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditEntry represents a single audit log record
type AuditEntry struct {
	ID           int64
	Timestamp    time.Time
	Username     string
	Action       string
	ResourceType string
	ResourceID   string
	Detail       string
	SourceIP     string
	StatusCode   int

	// #1678 — attribution columns. All default to "" (pre-migration rows
	// backfilled by ADD COLUMN, and rows written by a call site that
	// hasn't been updated to populate them).

	// Actor is the root human/service principal at the base of the
	// caller's delegation chain (auth.RootActor), distinct from Username
	// (the token's own subject — an agent's synthetic identity when the
	// call was delegated). Empty for a direct, non-delegated call, where
	// the acting entity IS Username; callers don't need to duplicate it.
	Actor string
	// DelegationChain is the JSON-serialized auth.Actor chain (empty when
	// there was no delegation), kept for full depth reconstruction beyond
	// what the flat Actor column can show.
	DelegationChain string
	// TokenID is the acting token's jti, so "what did this credential do"
	// is one query (QueryParams.TokenID).
	TokenID string
	// OrgID is tenant attribution. Empty on an OSS single-tenant daemon;
	// the cloud control plane's own audit path is out of scope here (see
	// FootprintAI/Containarium-cloud#1428).
	OrgID string
	// RunID groups a session's actions. Empty when the caller didn't
	// supply one.
	RunID string
}

// QueryParams holds parameters for querying audit logs
type QueryParams struct {
	Username     string
	Action       string
	ResourceType string
	// ResourceID is an exact-match filter on the row's resource id (#2440:
	// a tenant's network-policy audit rows carry the tenant here).
	ResourceID string
	// #1678 — attribution filters. Each is an exact-match equality filter,
	// same convention as Username/Action/ResourceType above.
	Actor   string
	TokenID string
	OrgID   string
	// RunID filters to one skill run's audit rows (#1818) — the
	// agent.run_lease_issue / agent.run_lease_end rows a run writes,
	// same exact-match convention as TokenID above.
	RunID  string
	From   time.Time
	To     time.Time
	Limit  int
	Offset int
}

// Store handles persistent storage of audit log entries
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new audit store connected to PostgreSQL
func NewStore(ctx context.Context, pool *pgxpool.Pool) (*Store, error) {
	store := &Store{pool: pool}

	if err := store.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize audit schema: %w", err)
	}

	return store, nil
}

// initSchema creates the database schema if it doesn't exist.
//
// Phase 4.5: row_hash + prev_hash columns implement the
// tamper-evidence chain. Added with ADD COLUMN IF NOT EXISTS so
// the upgrade is non-destructive — pre-existing rows have NULL
// hashes and the verifier treats them as "before chain was
// enabled" (skipped from the chain head).
func (s *Store) initSchema(ctx context.Context) error {
	schema := `
		CREATE TABLE IF NOT EXISTS audit_logs (
			id BIGSERIAL PRIMARY KEY,
			timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			username TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			resource_type TEXT NOT NULL DEFAULT '',
			resource_id TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			source_ip TEXT NOT NULL DEFAULT '',
			status_code INTEGER NOT NULL DEFAULT 0
		);

		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS row_hash TEXT NOT NULL DEFAULT '';
		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS prev_hash TEXT NOT NULL DEFAULT '';

		-- #1678: attribution columns. hash_version defaults every
		-- pre-existing row to HashVersion1 (Postgres applies a column
		-- DEFAULT to existing rows in place on ADD COLUMN) — Log() always
		-- writes CurrentHashVersion (HashVersion2) going forward, so
		-- VerifyChain can tell old rows from new ones without a separate
		-- migration script.
		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS actor TEXT NOT NULL DEFAULT '';
		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS delegation_chain TEXT NOT NULL DEFAULT '';
		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS token_id TEXT NOT NULL DEFAULT '';
		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS org_id TEXT NOT NULL DEFAULT '';
		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS run_id TEXT NOT NULL DEFAULT '';
		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS hash_version SMALLINT NOT NULL DEFAULT 1;

		-- #2415: idempotency key for externally sourced batches (sentinel SSH
		-- session records). NULL for every row written by Log. Deliberately NOT
		-- an input to computeRowHash at any version: it has no evidentiary
		-- value, it only makes a replayed batch a no-op, and keeping it out of
		-- the digest leaves every existing row and VerifyChain untouched.
		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS dedupe_key TEXT NULL;
		CREATE UNIQUE INDEX IF NOT EXISTS idx_audit_logs_dedupe_key
			ON audit_logs(dedupe_key) WHERE dedupe_key IS NOT NULL;

		CREATE INDEX IF NOT EXISTS idx_audit_logs_timestamp
			ON audit_logs(timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_audit_logs_username
			ON audit_logs(username);
		CREATE INDEX IF NOT EXISTS idx_audit_logs_action
			ON audit_logs(action);
		CREATE INDEX IF NOT EXISTS idx_audit_logs_resource_type
			ON audit_logs(resource_type);
		CREATE INDEX IF NOT EXISTS idx_audit_logs_actor
			ON audit_logs(actor);
		CREATE INDEX IF NOT EXISTS idx_audit_logs_token_id
			ON audit_logs(token_id);
		CREATE INDEX IF NOT EXISTS idx_audit_logs_org_id
			ON audit_logs(org_id);
		CREATE INDEX IF NOT EXISTS idx_audit_logs_run_id
			ON audit_logs(run_id);
	`

	_, err := s.pool.Exec(ctx, schema+chainBaseSchema)
	return err
}

// Log inserts a single audit log entry.
//
// Phase 4.5: each insert reads the latest row's row_hash inside
// a transaction, computes the new row's hash from its content
// plus that prev_hash, and writes both. SELECT FOR UPDATE on
// the tail row serializes concurrent writers so the chain
// stays well-ordered. Without the lock, two concurrent inserts
// could both reference the same prev_hash and produce a fork.
// auditChainLockKey is the advisory-lock key appends serialize on. An
// arbitrary constant; it only has to be unique among this database's advisory
// locks.
const auditChainLockKey int64 = 0x0A0D17C4A1

// defaultVerifyBatch mirrors VerifyChainSinceID's own internal default
// (limit<=0 -> 1000) — used by VerifyChainAgainstAnchor to advance fromID
// across multiple calls the same way the CLI's runAuditVerify loops.
const defaultVerifyBatch = 1000

func (s *Store) Log(ctx context.Context, entry *AuditEntry) error {
	_, err := s.Append(ctx, entry)
	return err
}

// Append is Log, returning the id of the row it appended. A Proof Pack seal
// (#2798) records that id so `proof-pack verify` can find its row.
func (s *Store) Append(ctx context.Context, entry *AuditEntry) (int64, error) {
	ts := entry.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	// Truncate to what Postgres can store before hashing.
	//
	// computeRowHash feeds Timestamp.UnixNano() into the digest, and the
	// column is `timestamp with time zone` — microsecond precision. So a
	// nanosecond-precision time hashed here comes back truncated, the
	// recomputed hash differs, and VerifyChainSinceID reports the row as
	// modified. Every row, always: the chain could never verify, so the
	// tamper-evidence it exists for could not distinguish a tampered log
	// from an intact one.
	//
	// Truncating first makes the hashed value identical to the stored one.
	ts = ts.Truncate(time.Microsecond)
	entry.Timestamp = ts

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("audit: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // best-effort cleanup; Commit() supersedes

	// Serialize appenders on the chain itself.
	//
	// `SELECT ... ORDER BY id DESC LIMIT 1 FOR UPDATE` does not do this,
	// though it reads as if it should. It locks the row that is the tail
	// *now*; it cannot lock a row that does not exist yet. Two transactions
	// therefore both read tail N, both insert, and both claim N as
	// predecessor — a fork. On an empty table it is worse: there is no row to
	// lock at all, so every concurrent first write claims genesis.
	//
	// An advisory lock has no such gap: it is taken on a constant, not on a
	// row, so it serializes writers regardless of what the table contains.
	// It releases on commit or rollback with the transaction.
	//
	// This serializes all audit appends against each other. That is the point
	// — a hash chain is inherently sequential, and a chain that forks under
	// load is not tamper-evident, only tamper-suggestive.
	prevHash, err := lockChainTail(ctx, tx)
	if err != nil {
		return 0, err
	}
	_, id, _, err := insertChained(ctx, tx, entry, prevHash, "")
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("audit: commit: %w", err)
	}
	return id, nil
}

// lockChainTail takes the chain's advisory lock for the rest of tx and
// returns the current tail's row_hash (HashEmpty on an empty table). Shared
// by Log and LogBatch so there is exactly one place that serializes
// appenders; see the comment in Log for why this is an advisory lock and
// not SELECT ... FOR UPDATE on the tail row.
func lockChainTail(ctx context.Context, tx pgx.Tx) (string, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, auditChainLockKey); err != nil {
		return "", fmt.Errorf("audit: lock chain: %w", err)
	}
	var prevHash string
	err := tx.QueryRow(ctx,
		`SELECT row_hash FROM audit_logs ORDER BY id DESC LIMIT 1`,
	).Scan(&prevHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return HashEmpty, nil // first row: no predecessor
		}
		return "", fmt.Errorf("audit: read chain tail: %w", err)
	}
	return prevHash, nil
}

// insertChained computes entry's row hash against prevHash and inserts it.
// dedupeKey == "" stores NULL (every plain Log row); a non-empty key makes
// the insert ON CONFLICT DO NOTHING and reports inserted=false when the key
// already exists, in which case nothing is written and the caller must keep
// prevHash unchanged, so a skipped duplicate never becomes anyone's
// predecessor. The caller must hold the chain lock. id is the inserted row's
// id (0 when nothing was inserted).
func insertChained(ctx context.Context, tx pgx.Tx, entry *AuditEntry, prevHash, dedupeKey string) (rowHash string, id int64, inserted bool, err error) {
	// #1678 — every newly-written row uses CurrentHashVersion; a row's
	// hash_version is what VerifyChain later replays it under, so old
	// rows (backfilled to HashVersion1 by initSchema's column default)
	// stay verifiable without this write path ever touching them.
	rowHash, err = computeRowHash(entry, prevHash, CurrentHashVersion)
	if err != nil {
		return "", 0, false, fmt.Errorf("audit: compute hash: %w", err)
	}

	var key any // nil -> NULL
	conflict := ""
	if dedupeKey != "" {
		key = dedupeKey
		conflict = ` ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`
	}
	// A skipped duplicate (ON CONFLICT DO NOTHING) returns no row.
	err = tx.QueryRow(ctx, `
		INSERT INTO audit_logs (
			timestamp, username, action, resource_type, resource_id,
			detail, source_ip, status_code, actor, delegation_chain,
			token_id, org_id, run_id, row_hash, prev_hash, hash_version, dedupe_key
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`+conflict+` RETURNING id`,
		entry.Timestamp,
		entry.Username,
		entry.Action,
		entry.ResourceType,
		entry.ResourceID,
		entry.Detail,
		entry.SourceIP,
		entry.StatusCode,
		entry.Actor,
		entry.DelegationChain,
		entry.TokenID,
		entry.OrgID,
		entry.RunID,
		rowHash,
		prevHash,
		CurrentHashVersion,
		key,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return rowHash, 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("audit: insert: %w", err)
	}
	return rowHash, id, true, nil
}

// BatchEntry is one row of a LogBatch call. DedupeKey must be non-empty:
// it is what makes the row idempotent.
type BatchEntry struct {
	Entry     AuditEntry
	DedupeKey string
}

// BatchOutcome is LogBatch's per-entry result.
type BatchOutcome int

const (
	// BatchInserted: the row was appended to the chain.
	BatchInserted BatchOutcome = iota + 1
	// BatchDuplicate: an entry with this DedupeKey already exists; nothing
	// was written.
	BatchDuplicate
)

// LogBatch appends entries in order inside ONE transaction, taking the
// chain's advisory lock once for the whole batch (#2415). It returns one
// outcome per entry, in order. On any error nothing is committed, so a
// caller can retry the identical batch: entries that already exist report
// BatchDuplicate and are skipped before they can become a predecessor, so a
// replay never forks or pads the chain.
func (s *Store) LogBatch(ctx context.Context, entries []BatchEntry) ([]BatchOutcome, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	for i := range entries {
		if entries[i].DedupeKey == "" {
			return nil, fmt.Errorf("audit: batch entry %d has no dedupe key", i)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("audit: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // best-effort cleanup; Commit() supersedes

	prevHash, err := lockChainTail(ctx, tx)
	if err != nil {
		return nil, err
	}

	outcomes := make([]BatchOutcome, len(entries))
	for i := range entries {
		e := entries[i].Entry // copy: don't mutate the caller's slice
		if e.Timestamp.IsZero() {
			e.Timestamp = time.Now()
		}
		// Truncate to what Postgres stores before hashing (see Log).
		e.Timestamp = e.Timestamp.Truncate(time.Microsecond)

		rowHash, _, inserted, err := insertChained(ctx, tx, &e, prevHash, entries[i].DedupeKey)
