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
