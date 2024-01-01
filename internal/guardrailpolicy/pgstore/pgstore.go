// Package pgstore is the Postgres guardrailpolicy.Store. It is a separate
// package so the containarium client binary, which uses guardrailpolicy for
// validation and display, does not link the database driver.
package pgstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Store keeps the policy in a singleton row. The row is seeded at
// bootstrap with a NULL policy, which is the stored "not configured" state;
// every Set locks that row, so concurrent Sets serialize and the revision
// never repeats.
type Store struct {
	pool *pgxpool.Pool
}

// New bootstraps the table and its singleton row on a pool the
// daemon already owns. Idempotent: an existing row (and its revision) is kept.
func New(ctx context.Context, pool *pgxpool.Pool) (*Store, error) {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS guardrail_policy (
			id         SMALLINT PRIMARY KEY CHECK (id = 1),
			revision   BIGINT NOT NULL DEFAULT 0,
			policy     BYTEA,
			updated_at TIMESTAMPTZ,
			updated_by TEXT NOT NULL DEFAULT ''
		);
