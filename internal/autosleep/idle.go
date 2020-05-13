package autosleep

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// trafficStorePool is the narrowed pgxpool surface the traffic adapter
// touches. Declared here so this file doesn't import the larger
// *traffic.Store directly — keeps the package free of any traffic.*
// type dependency at the public API level.
type trafficStorePool interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// TrafficStoreAdapter satisfies TrafficSource over a pgx pool, querying
// traffic_connections for the most recent activity timestamp per
// container. Constructed by daemon wiring from *traffic.Store's
// underlying pool.
//
// A nil receiver is intentionally accepted by the Manager: daemons
// without a traffic store skip the network signal entirely and fall
// back to the "since-start" branch of Decide.
type TrafficStoreAdapter struct {
	pool trafficStorePool
}

// NewTrafficStoreAdapter wraps a pgx pool for use as a TrafficSource.
// The container_name column in traffic_connections is the full Incus
// name ("alice-container"), so callers pass the same.
func NewTrafficStoreAdapter(pool *pgxpool.Pool) *TrafficStoreAdapter {
	if pool == nil {
		return nil
	}
	return &TrafficStoreAdapter{pool: pool}
