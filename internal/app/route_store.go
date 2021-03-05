package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrRouteNotFound is returned when a route is not found
	ErrRouteNotFound = errors.New("route not found")

	// ErrRouteOwnershipConflict is returned by Save when full_domain
	// already belongs to a different creator. See Save's doc comment.
	ErrRouteOwnershipConflict = errors.New("route ownership conflict")
)

// RouteCreator identifies who created a route.
type RouteCreator string

const (
	// RouteCreatorSystem marks routes created automatically by the daemon (e.g. management UI).
	RouteCreatorSystem RouteCreator = "system"
	// RouteCreatorUser marks routes created by a user via the API.
	RouteCreatorUser RouteCreator = "user"
)

// RouteRecord represents a route stored in PostgreSQL (source of truth)
type RouteRecord struct {
	ID            string
	Subdomain     string
	FullDomain    string
	TargetIP      string
	TargetPort    int
	Protocol      string // "http" or "grpc"
	ContainerName string
	AppID         *string // nullable, references apps table
	Description   string
	Active        bool
	CreatedBy     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// RouteStore handles persistent storage of routes using PostgreSQL
type RouteStore struct {
	pool *pgxpool.Pool
}

// NewRouteStore creates a new route store using an existing connection pool
func NewRouteStore(ctx context.Context, pool *pgxpool.Pool) (*RouteStore, error) {
	store := &RouteStore{pool: pool}

	// Initialize schema
	if err := store.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize route schema: %w", err)
	}

	return store, nil
}

// initSchema creates the routes table if it doesn't exist
func (s *RouteStore) initSchema(ctx context.Context) error {
	// Ensure apps table exists (at least the id column) so the FK constraint works.
	// When app hosting is enabled, the full apps table is created by app.Store.
	// When it's not, we create a minimal stub so routes can still reference it.
	appsStub := `
		CREATE TABLE IF NOT EXISTS apps (
			id UUID PRIMARY KEY,
			data JSONB NOT NULL DEFAULT '{}',
			username TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT '',
			subdomain TEXT UNIQUE NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 0,
			container_name TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			deployed_at TIMESTAMP
		);
	`
	if _, err := s.pool.Exec(ctx, appsStub); err != nil {
		return fmt.Errorf("failed to ensure apps table: %w", err)
	}

	schema := `
		CREATE TABLE IF NOT EXISTS routes (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			subdomain TEXT NOT NULL,
			full_domain TEXT UNIQUE NOT NULL,
			target_ip TEXT NOT NULL,
			target_port INTEGER NOT NULL,
			protocol TEXT NOT NULL DEFAULT 'http',
			container_name TEXT,
			app_id UUID REFERENCES apps(id) ON DELETE SET NULL,
			description TEXT,
			active BOOLEAN NOT NULL DEFAULT true,
			created_by TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_routes_subdomain ON routes(subdomain);
		CREATE INDEX IF NOT EXISTS idx_routes_active ON routes(active);
		CREATE INDEX IF NOT EXISTS idx_routes_app_id ON routes(app_id);
		CREATE INDEX IF NOT EXISTS idx_routes_full_domain ON routes(full_domain);
	`

	_, err := s.pool.Exec(ctx, schema)
	return err
}

// Save saves or updates a route (upsert by full_domain).
//
// A hostname already owned by a DIFFERENT creator is refused with
// ErrRouteOwnershipConflict rather than silently rebound. AddRoute is
// admin-only, but nothing previously stopped an admin — or an automated
// reconciliation path, or a future caller — from naming a hostname that
// routes to another tenant's container and silently repointing it there.
// The check is inside SELECT ... FOR UPDATE + the upsert's own
// transaction, so two concurrent Saves of the same full_domain can't
// both observe "no conflict" and race each other into it.
//
// Rows with no recorded creator (created_by empty — every row written
// before this check existed) are exempt from the *refusal*, so an
// upgrade never locks operators out of routes they already have; the
// upsert does backfill created_by for such a row on its first touch
// after upgrade (COALESCE(NULLIF(...))) below), so enforcement still
// converges forward instead of leaving every pre-existing route
// permanently unprotected.
//
// To intentionally reassign a hostname to a different creator, delete
// the existing route first (DeleteRoute) and create the new one —
// there's no override flag; an admin already has both operations.
func (s *RouteStore) Save(ctx context.Context, route *RouteRecord) error {
	now := time.Now()
	if route.CreatedAt.IsZero() {
		route.CreatedAt = now
	}
	route.UpdatedAt = now

	if route.Protocol == "" {
		route.Protocol = "http"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to save route: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once Commit succeeds

	var existingCreatedBy string
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(created_by, '') FROM routes WHERE full_domain = $1 FOR UPDATE`,
		route.FullDomain,
	).Scan(&existingCreatedBy)
	switch {
	case err == nil:
		if existingCreatedBy != "" && existingCreatedBy != route.CreatedBy {
			return fmt.Errorf("%w: %s is owned by a different creator", ErrRouteOwnershipConflict, route.FullDomain)
		}
	case errors.Is(err, pgx.ErrNoRows):
