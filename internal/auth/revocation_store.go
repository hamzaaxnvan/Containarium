//go:build !containarium_client

package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgRevocationStore is the Postgres-backed implementation of
// RevocationStore. Single table, single index on expires_at
// for cleanup, primary key on jti for the hot-path lookup.
type PgRevocationStore struct {
	pool *pgxpool.Pool
}

// NewPgRevocationStore creates the store and ensures the
// schema is in place. ADD COLUMN IF NOT EXISTS would be
// non-destructive on existing deployments — the table itself
// uses CREATE TABLE IF NOT EXISTS.
func NewPgRevocationStore(ctx context.Context, pool *pgxpool.Pool) (*PgRevocationStore, error) {
	s := &PgRevocationStore{pool: pool}
	if err := s.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("revocation store: init schema: %w", err)
	}
	return s, nil
}

func (s *PgRevocationStore) initSchema(ctx context.Context) error {
	schema := `
		CREATE TABLE IF NOT EXISTS jwt_revocations (
			jti         TEXT PRIMARY KEY,
			expires_at  TIMESTAMPTZ NOT NULL,
			revoked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			reason      TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_jwt_revocations_expires_at
			ON jwt_revocations(expires_at);

		-- Refresh-token rotation families (reuse-detection). No expiry
		-- column: a family is only ever revoked in response to a
		-- detected replay, and that verdict must stick for the life of
		-- the family, not get pruned on a timer the way an individual
		-- jti's revocation row does.
		CREATE TABLE IF NOT EXISTS jwt_revoked_families (
			family_id   TEXT PRIMARY KEY,
			revoked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			reason      TEXT NOT NULL DEFAULT ''
		);
	`
	_, err := s.pool.Exec(ctx, schema)
	return err
}

// IsRevoked returns true when the row exists. We don't filter
// by expires_at here — a revoked token past its exp would have
// already been rejected by the JWT exp validator, but if the
// row hasn't been cleaned up yet there's no harm in answering
// "revoked" for it (the answer is technically correct and the
// caller's JWT exp check is the real gate).
func (s *PgRevocationStore) IsRevoked(ctx context.Context, jti string) (bool, error) {
	if jti == "" {
		return false, nil
