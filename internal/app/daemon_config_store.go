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
