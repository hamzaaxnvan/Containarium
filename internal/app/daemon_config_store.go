package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DaemonConfigStore handles persistent storage of daemon configuration using PostgreSQL.
// It stores key-value pairs so the daemon can self-bootstrap after VM recreation.
type DaemonConfigStore struct {
	pool *pgxpool.Pool
}

// NewDaemonConfigStore creates a new daemon config store and initializes the schema.
func NewDaemonConfigStore(ctx context.Context, pool *pgxpool.Pool) (*DaemonConfigStore, error) {
	store := &DaemonConfigStore{pool: pool}
	if err := store.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize daemon_config schema: %w", err)
	}
	return store, nil
}

func (s *DaemonConfigStore) initSchema(ctx context.Context) error {
	schema := `
		CREATE TABLE IF NOT EXISTS daemon_config (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		);
	`
	_, err := s.pool.Exec(ctx, schema)
	return err
}

// ErrDaemonConfigNotFound reports that no value is stored under a key.
//
// A sentinel because absence is a normal answer here and has to be
// distinguishable from a database failure: the daemon reads this store while
// bootstrapping, and "nothing configured yet" means fall back to defaults
// while "the database is unreachable" means do not.
//
// It wraps pgx.ErrNoRows rather than replacing it, so callers already
// matching on that keep working.
var ErrDaemonConfigNotFound = errors.New("no daemon config stored under that key")

// Get retrieves a single config value by key.
//
// A missing key is an ERROR wrapping ErrDaemonConfigNotFound, not an empty
// string — the doc used to claim otherwise, and it was wrong (#1300). The
// distinction matters: a stored empty value is a real setting ("switched
// off"), and collapsing it together with absence and with a failed query
// would have the daemon boot on defaults believing they were configured.
func (s *DaemonConfigStore) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := s.pool.QueryRow(ctx,
		"SELECT value FROM daemon_config WHERE key = $1", key,
	).Scan(&value)
