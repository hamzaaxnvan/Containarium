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
