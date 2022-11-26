package tracker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
)

// Connection is the storage-layer view of a TrackerConnection. Provider is
// the typed enum, not the DB's TEXT column — the string<->enum conversion
// happens at this store boundary (providerToString / providerFromString),
// per the "protobuf enums over magic strings" convention: every caller
// above this package sees pb.TrackerProvider, never a raw string.
type Connection struct {
	Username         string
	Name             string
	Provider         pb.TrackerProvider
	BaseURL          string
	Project          string
	CredentialSecret string
	// CredentialExpiresAt is surfaced, not enforced — populated
	// best-effort from the provider's own introspection of the
	// credential (DescribeCredential) at connect time and refreshed by
	// GetTrackerStatus. Zero means "unknown or no expiry", not "never
	// expires".
	CredentialExpiresAt time.Time
	// Policy is the connection's guardrails for agent-filed follow-ups
	// (#2024), stored as protojson in a JSONB column and read back
	// through the generated decoder. nil when the operator never set
	// one — callers apply the documented defaults via PolicyFromProto,
	// which treats nil and zero-valued identically.
	Policy    *pb.TrackerPolicy
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ErrNotFound is returned by Get / Delete when the (username, name) tuple
// has no row.
var ErrNotFound = errors.New("tracker: connection not found")

// Store handles per-tenant tracker-connection persistence. Holds no
// credential material itself — CredentialSecret only names a secret that
// lives in internal/secrets, in SECRET_DELIVERY_BROKER_ONLY mode; that
// cross-package validation happens in internal/server (this package has
// no dependency on internal/secrets).
type Store struct {
	pool *pgxpool.Pool
	// runGates serializes RecordChild's reservation step per run inside
	// this process, BEFORE a pool connection is taken, so same-run
	// creates queue without holding connections (#2044).
	runGates runGates
}

// NewStore opens the tracker-connections store, creating the table on
// first run. Idempotent on every subsequent call, same idiom as
// internal/secrets.NewStore. Safe to call from several processes at once
// (#2061): see initSchema.
func NewStore(ctx context.Context, pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("tracker: pool is nil")
	}
	s := &Store{pool: pool}
	if err := s.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return s, nil
}

// schemaLockKey is the advisory-lock key concurrent initSchema calls
// serialize on — one constant for the whole tracker schema, hashed the
// same way as reserveChild's per-run key. It is only ever taken inside
// initSchema's transaction, so it costs nothing after startup.
const schemaLockKey = "tracker/schema"

// initSchema creates or upgrades the tracker tables, in one transaction
// under a transaction-scoped advisory lock (#2061).
//
// CREATE TABLE IF NOT EXISTS is not safe to race: it checks pg_class and
// then inserts the table's row type into pg_type, so two sessions that
// both passed the check collide on pg_type_typname_nsp_index (SQLSTATE
// 23505) and the loser's whole batch rolls back. Nothing in the first-run
// path serialized the sessions before that point: the upgrade path only
// looked safe because ALTER TABLE ... ADD COLUMN takes an ACCESS EXCLUSIVE
// lock on tracker_connections and holds it to the end of ITS implicit
// transaction, which covered the CREATE after it in the same batch — and
// nothing in the two batches that follow.
//
// So the lock has to cover every CREATE in every batch, not just the
// first-run one. Taking it once around the whole init does that: the
// winner creates everything, each later holder sees the tables committed
// and every statement becomes the no-op its IF NOT EXISTS promises. It
// is transaction-scoped, same as the lineage lock in reserveChild, so it
// releases at commit or rollback and never touches a query afterward;
// on an already-initialized database it adds one lock round trip to a
// path that runs once per process start.
func (s *Store) initSchema(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin schema transaction: %w", err)
	}
	// No-op after a successful Commit. Detached so a cancelled context
	// still releases the transaction (and with it the lock) cleanly.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, schemaLockKey); err != nil {
		return fmt.Errorf("lock schema: %w", err)
	}
	if err := applySchema(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit schema: %w", err)
	}
	return nil
}

// applySchema runs the three schema batches, in dependency order, on tx.
func applySchema(ctx context.Context, tx pgx.Tx) error {
	const schema = `
		CREATE TABLE IF NOT EXISTS tracker_connections (
			username          TEXT NOT NULL,
			name              TEXT NOT NULL,
			provider          TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
			base_url          TEXT NOT NULL DEFAULT '',
			project           TEXT NOT NULL,
			credential_secret TEXT NOT NULL,
			created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (username, name)
		);

		-- #1921 step 3: added after initial release — idempotent
		-- ALTER, same idiom as internal/secrets.Store.initSchema.
		ALTER TABLE tracker_connections ADD COLUMN IF NOT EXISTS credential_expires_at TIMESTAMPTZ;

		-- #2024: connection policy (protojson of TrackerPolicy; NULL =
		-- never set = defaults).
		ALTER TABLE tracker_connections ADD COLUMN IF NOT EXISTS policy JSONB;

		-- #2024: which run filed which follow-up under which parent, and
		-- how deep. A human-created issue has no row (depth 0).
		CREATE TABLE IF NOT EXISTS tracker_issue_lineage (
			username       TEXT   NOT NULL,
			connection     TEXT   NOT NULL,
			child_number   BIGINT NOT NULL,
			parent_number  BIGINT NOT NULL,
			created_by_run TEXT   NOT NULL,
			depth          INT    NOT NULL,
			PRIMARY KEY (username, connection, child_number)
		);

		-- #2044: a fan-out slot claimed by an in-flight RecordChild whose
		-- upstream create has not finished yet. Counted with the lineage
		-- rows by the fan-out guard, so the upstream call can run with no
		-- transaction (and no pool connection) held. Deleted when the
		-- create fails or its lineage row is recorded.
		CREATE TABLE IF NOT EXISTS tracker_lineage_reservations (
			id             BIGSERIAL   PRIMARY KEY,
			username       TEXT        NOT NULL,
			connection     TEXT        NOT NULL,
			created_by_run TEXT        NOT NULL,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS tracker_lineage_reservations_run
			ON tracker_lineage_reservations (username, connection, created_by_run);
	`
	if _, err := tx.Exec(ctx, schema); err != nil {
		return err
	}
	// #2021: scope routes hang off a connection by foreign key, so they
	// are created after tracker_connections.
	if _, err := tx.Exec(ctx, routeSchema); err != nil {
		return err
	}
	// #2022: dispatch rows and unrouted-scope warnings, also keyed off a
	// connection.
	_, err := tx.Exec(ctx, dispatchSchema)
	return err
}

// providerToString converts the typed enum to the DB's stored string.
// Rejects UNSPECIFIED (and anything else unrecognized) — provider is
// never inferred or defaulted, per the design note.
func providerToString(p pb.TrackerProvider) (string, error) {
	switch p {
	case pb.TrackerProvider_TRACKER_PROVIDER_GITHUB:
		return "github", nil
	case pb.TrackerProvider_TRACKER_PROVIDER_GITLAB:
		return "gitlab", nil
	default:
		return "", fmt.Errorf("tracker: provider must be GITHUB or GITLAB, got %v", p)
	}
}

// providerFromString is providerToString's inverse, used when reading a
// row back. An unrecognized string (should be unreachable given the CHECK
// constraint and providerToString gating every write) maps to
// UNSPECIFIED rather than guessing.
func providerFromString(s string) pb.TrackerProvider {
	switch s {
	case "github":
		return pb.TrackerProvider_TRACKER_PROVIDER_GITHUB
	case "gitlab":
		return pb.TrackerProvider_TRACKER_PROVIDER_GITLAB
	default:
		return pb.TrackerProvider_TRACKER_PROVIDER_UNSPECIFIED
	}
}

// Set creates or updates a tracker connection. Idempotent — repeated
// calls with the same (username, name) replace every field.
func (s *Store) Set(ctx context.Context, c Connection) (*Connection, error) {
	if c.Username == "" {
		return nil, errors.New("tracker: username is required")
	}
	if c.Name == "" {
		return nil, errors.New("tracker: name is required")
	}
	if c.Project == "" {
		return nil, errors.New("tracker: project is required")
	}
	if c.CredentialSecret == "" {
		return nil, errors.New("tracker: credential_secret is required")
	}
