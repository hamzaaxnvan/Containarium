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

// upgrade brings data an earlier release stored into line with today's
// rules, inside the bootstrap transaction. Every change is logged and
// recorded in retention_upgrade_events in the same transaction, so it can
// be audited once an audit store exists (Q31; see AuditUpgradeEvents).
//
//   - A window above retention.MaxDays (possible before the cap existed) is
//     clamped to the cap, the policy revision is bumped (updated_by
//     retention.UpgradeActor), then the cap's CHECK is validated. Failing
//     the bootstrap instead would leave every retention call UNAVAILABLE.
//   - A hold whose org id has surrounding whitespace (possible before ids
//     were trimmed) is stored under the trimmed id, so it can be released
//     by that id and the sweepers match it against record org ids. When
//     several stored ids trim to the same org, the earliest hold is kept and
//     each other one is removed; its record keeps it in full and the org
//     stays held.
//   - A hold whose org id is empty once trimmed is quarantined: re-stored,
//     still active, under retention.QuarantineOrgID, so an admin can release
//     it. It is not dropped: while any hold is active, records with no org
//     are held, and dropping it could release them.
func upgrade(ctx context.Context, tx pgx.Tx) error {
	if err := clampWindows(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE retention_windows VALIDATE CONSTRAINT retention_windows_days_max`); err != nil {
		return fmt.Errorf("validate the retention window cap: %w", err)
	}
	return normaliseHoldIDs(ctx, tx)
}

// recordUpgrade stores one upgrade change for its later audit entry.
func recordUpgrade(ctx context.Context, tx pgx.Tx, action, resourceType, resourceID string, detail any) error {
	payload, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("record %s: %w", action, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO retention_upgrade_events (action, resource_type, resource_id, detail)
		VALUES ($1, $2, $3, $4)`, action, resourceType, resourceID, string(payload)); err != nil {
		return fmt.Errorf("record %s: %w", action, err)
	}
	return nil
}

// clampWindows clamps windows above the cap; see upgrade.
func clampWindows(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `WITH old AS (
			SELECT class, days FROM retention_windows WHERE days > $1 FOR UPDATE
		)
		UPDATE retention_windows w SET days = $1 FROM old WHERE w.class = old.class
		RETURNING w.class, old.days`, retention.MaxDays)
	if err != nil {
		return fmt.Errorf("clamp retention windows: %w", err)
	}
	var clamped []retention.UpgradeClampedWindow
	for rows.Next() {
		var class, days int32
		if err := rows.Scan(&class, &days); err != nil {
			rows.Close()
			return fmt.Errorf("clamp retention windows: %w", err)
		}
		clamped = append(clamped, retention.UpgradeClampedWindow{Class: pb.RetentionClass(class).String(), StoredDays: days, Days: retention.MaxDays})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("clamp retention windows: %w", err)
	}
	if len(clamped) == 0 {
		return nil
	}
	sort.Slice(clamped, func(i, j int) bool { return clamped[i].Class < clamped[j].Class })
	d := retention.UpgradeClampDetail{Windows: clamped}
	if err := tx.QueryRow(ctx, `UPDATE retention_policy SET revision = revision + 1, updated_at = $1, updated_by = $2
		WHERE id = 1 RETURNING revision`, retention.Now(), retention.UpgradeActor).Scan(&d.Revision); err != nil {
		return fmt.Errorf("bump the retention policy revision: %w", err)
	}
	d.PreviousRevision = d.Revision - 1
	for _, w := range clamped {
		log.Printf("Retention: clamped the stored %s window from %d to %d days (the cap); policy revision %d", w.Class, w.StoredDays, w.Days, d.Revision)
	}
	return recordUpgrade(ctx, tx, retention.UpgradeActionClamp, "retention_policy", "cluster", d)
}

// normaliseHoldIDs trims stored hold org ids the way retention.NormalizeHold
// does, and quarantines ids that trim to nothing; see upgrade.
func normaliseHoldIDs(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT org_id, reason, set_by, set_at FROM retention_legal_holds ORDER BY set_at, org_id`)
	if err != nil {
		return fmt.Errorf("read legal holds: %w", err)
	}
	var holds []*pb.LegalHold
	for rows.Next() {
		h, err := scanHold(rows)
		if err != nil {
			rows.Close()
			return fmt.Errorf("read legal holds: %w", err)
		}
		holds = append(holds, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read legal holds: %w", err)
	}
	// Group by the id each hold should have; holds are ordered by set_at, so
	// the first of a group is the earliest and is the one kept.
	groups := map[string][]*pb.LegalHold{}
	var order []string
