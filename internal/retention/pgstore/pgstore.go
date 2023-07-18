// Package pgstore is the Postgres retention.Store. It is a separate package
// so the containarium client binary does not link the database driver.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/retention"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Store keeps the mode in a singleton row, the policy bookkeeping in another (locked by every set,
// so revisions never repeat), the set windows one row per class, and the
// active holds one row per org. A released hold's row is deleted; its history
// is in the audit log.
type Store struct {
	pool *pgxpool.Pool
}

// bootstrapLockKey serializes concurrent bootstraps (daemons starting
// together) so the constraint upgrade and the hold-id normalisation below
// never race each other.
const bootstrapLockKey = "containarium.retention.bootstrap"

// New bootstraps the tables on a pool the daemon already owns. Idempotent,
// and safe when several daemons start at once. Over a schema an earlier
// release created, it also upgrades the data in place (see upgrade).
func New(ctx context.Context, pool *pgxpool.Pool) (*Store, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin retention bootstrap: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, bootstrapLockKey); err != nil {
		return nil, fmt.Errorf("lock retention bootstrap: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS retention_policy (
			id         SMALLINT PRIMARY KEY CHECK (id = 1),
			revision   BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ,
			updated_by TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO retention_policy (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
		CREATE TABLE IF NOT EXISTS retention_windows (
			class INTEGER PRIMARY KEY,
			days  INTEGER NOT NULL CHECK (days > 0)
		);
		-- The upper bound (retention.MaxDays) as a named constraint. It is
		-- added NOT VALID, so existing rows are not checked yet: upgrade
		-- clamps any row above the cap, then validates the constraint.
		DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_constraint
				WHERE conrelid = 'retention_windows'::regclass AND conname = 'retention_windows_days_max') THEN
				ALTER TABLE retention_windows ADD CONSTRAINT retention_windows_days_max CHECK (days <= `+maxDaysSQL+`) NOT VALID;
			END IF;
		END $$;
		-- The daemon-wide mode: a singleton row, DRY_RUN (1) until an admin sets
		-- it. updated_at stays NULL until the first set.
		CREATE TABLE IF NOT EXISTS retention_mode (
			id         SMALLINT PRIMARY KEY CHECK (id = 1),
			mode       INTEGER NOT NULL DEFAULT 1 CHECK (mode IN (1, 2)),
			updated_at TIMESTAMPTZ,
			updated_by TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO retention_mode (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
		CREATE TABLE IF NOT EXISTS retention_legal_holds (
			org_id TEXT PRIMARY KEY,
			reason TEXT NOT NULL,
			set_by TEXT NOT NULL,
			set_at TIMESTAMPTZ NOT NULL
		);
		-- Changes the bootstrap made to data an earlier release stored, kept
		-- until each has its audit entry (Q31; see upgrade).
		CREATE TABLE IF NOT EXISTS retention_upgrade_events (
			id            BIGSERIAL PRIMARY KEY,
			action        TEXT NOT NULL,
			resource_type TEXT NOT NULL,
			resource_id   TEXT NOT NULL,
			detail        TEXT NOT NULL,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
			audited_at    TIMESTAMPTZ
		);
	`); err != nil {
		return nil, fmt.Errorf("init retention schema: %w", err)
	}
	if err := upgrade(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit retention bootstrap: %w", err)
	}
	return &Store{pool: pool}, nil
}
