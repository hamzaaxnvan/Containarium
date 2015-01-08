package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AlertRule represents a custom alert rule persisted in PostgreSQL
type AlertRule struct {
	ID          string
	Name        string
	Expr        string
	Duration    string
	Severity    string
	Description string
	Labels      map[string]string
	Annotations map[string]string
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Store handles persistent storage of alert rules
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new alert store connected to PostgreSQL
func NewStore(ctx context.Context, pool *pgxpool.Pool) (*Store, error) {
	store := &Store{pool: pool}

	if err := store.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize alert schema: %w", err)
	}

	return store, nil
}

// initSchema creates the database schema if it doesn't exist
func (s *Store) initSchema(ctx context.Context) error {
	schema := `
		CREATE TABLE IF NOT EXISTS alert_rules (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name TEXT NOT NULL,
			expr TEXT NOT NULL,
			duration TEXT NOT NULL DEFAULT '5m',
			severity TEXT NOT NULL DEFAULT 'warning',
			description TEXT NOT NULL DEFAULT '',
			labels JSONB NOT NULL DEFAULT '{}',
			annotations JSONB NOT NULL DEFAULT '{}',
			enabled BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_alert_rules_enabled
			ON alert_rules(enabled);
		CREATE INDEX IF NOT EXISTS idx_alert_rules_severity
			ON alert_rules(severity);
	`

	_, err := s.pool.Exec(ctx, schema)
	return err
