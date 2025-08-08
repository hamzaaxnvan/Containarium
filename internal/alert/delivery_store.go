package alert

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// WebhookDelivery represents a single webhook delivery attempt
type WebhookDelivery struct {
	ID           int64
	Timestamp    time.Time
	AlertName    string
	Source       string // "relay" or "test"
	WebhookURL   string // masked
	Success      bool
	HTTPStatus   int
	ErrorMessage string
	PayloadSize  int
	DurationMs   int
}

// DeliveryStore handles persistent storage of webhook delivery attempts
type DeliveryStore struct {
	pool *pgxpool.Pool

	// Debounce cleanup: only run once per interval
	cleanupMu   sync.Mutex
	lastCleanup time.Time
}

// NewDeliveryStore creates a new delivery store sharing the given pool
func NewDeliveryStore(ctx context.Context, pool *pgxpool.Pool) (*DeliveryStore, error) {
	store := &DeliveryStore{pool: pool}
	if err := store.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize delivery schema: %w", err)
	}
	return store, nil
}

func (s *DeliveryStore) initSchema(ctx context.Context) error {
	schema := `
		CREATE TABLE IF NOT EXISTS webhook_deliveries (
			id BIGSERIAL PRIMARY KEY,
			timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			alert_name TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
