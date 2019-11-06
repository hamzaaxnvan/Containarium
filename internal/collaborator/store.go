package collaborator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/footprintai/containarium/internal/safecast"
)

var (
	// ErrNotFound is returned when a collaborator is not found
	ErrNotFound = errors.New("collaborator not found")

	// ErrAlreadyExists is returned when trying to add a collaborator that already exists
	ErrAlreadyExists = errors.New("collaborator already exists")
)

// Collaborator represents a user with access to a container
type Collaborator struct {
	ID                   string
	ContainerName        string
	OwnerUsername        string
	CollaboratorUsername string
	AccountName          string // e.g., "alice-container-bob"
	SSHPublicKey         string
	CreatedAt            time.Time
	CreatedBy            string
	HasSudo              bool // Full sudo access (not just su - owner)
	HasContainerRuntime  bool // Docker/podman group membership
}

// Store handles persistent storage of collaborators using PostgreSQL
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new collaborator store connected to PostgreSQL
// connectionString format: postgres://user:password@host:port/database?sslmode=disable
func NewStore(ctx context.Context, connectionString string) (*Store, error) {
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Test the connection under a bounded deadline. pgx's dial has no timeout of
	// its own, so an unreachable Postgres would otherwise block here forever —
	// which stalls daemon startup before it binds its servers (#1007).
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	store := &Store{pool: pool}

	// Initialize schema
	if err := store.initSchema(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return store, nil
}

// Pool returns the store's connection pool, so other daemon tables (the
// ssh_keys registry, #2517) share it.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Close closes the database connection pool
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// initSchema creates the database schema if it doesn't exist
func (s *Store) initSchema(ctx context.Context) error {
	schema := `
		CREATE TABLE IF NOT EXISTS collaborators (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			container_name TEXT NOT NULL,
			owner_username TEXT NOT NULL,
			collaborator_username TEXT NOT NULL,
			account_name TEXT NOT NULL,
			ssh_public_key TEXT NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			created_by TEXT,
			UNIQUE(container_name, collaborator_username)
		);

		CREATE INDEX IF NOT EXISTS idx_collaborators_container ON collaborators(container_name);
		CREATE INDEX IF NOT EXISTS idx_collaborators_owner ON collaborators(owner_username);
		CREATE INDEX IF NOT EXISTS idx_collaborators_collaborator ON collaborators(collaborator_username);
		CREATE INDEX IF NOT EXISTS idx_collaborators_account ON collaborators(account_name);

		ALTER TABLE collaborators ADD COLUMN IF NOT EXISTS has_sudo BOOLEAN DEFAULT FALSE;
		ALTER TABLE collaborators ADD COLUMN IF NOT EXISTS has_container_runtime BOOLEAN DEFAULT FALSE;
	`

	_, err := s.pool.Exec(ctx, schema)
	return err
}

// Add adds a new collaborator to the database
func (s *Store) Add(ctx context.Context, c *Collaborator) error {
	query := `
		INSERT INTO collaborators (container_name, owner_username, collaborator_username, account_name, ssh_public_key, created_at, created_by, has_sudo, has_container_runtime)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`

	createdAt := c.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	err := s.pool.QueryRow(ctx, query,
		c.ContainerName,
		c.OwnerUsername,
		c.CollaboratorUsername,
		c.AccountName,
		c.SSHPublicKey,
		createdAt,
		c.CreatedBy,
		c.HasSudo,
		c.HasContainerRuntime,
