package traffic

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/safecast"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Store handles persistent storage of traffic data using PostgreSQL
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new traffic store connected to PostgreSQL
// connectionString format: postgres://user:password@host:port/database?sslmode=disable
func NewStore(ctx context.Context, connectionString string) (*Store, error) {
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Test the connection under a bounded deadline: pgx's dial has no timeout
	// of its own, so an unresponsive Postgres would block daemon startup
	// before it serves (#2824).
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

// Close closes the database connection pool
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// Pool exposes the underlying pgx pool for callers that need to issue
// custom queries (e.g. the autosleep package's LastNetworkActivity probe
// over traffic_connections). Returns nil if the store wasn't initialized.
func (s *Store) Pool() *pgxpool.Pool {
	if s == nil {
		return nil
	}
	return s.pool
}

// initSchema creates the database schema if it doesn't exist
func (s *Store) initSchema(ctx context.Context) error {
	schema := `
		-- Connection history table for long-term storage
		CREATE TABLE IF NOT EXISTS traffic_connections (
			id BIGSERIAL PRIMARY KEY,
			container_name TEXT NOT NULL,
			protocol SMALLINT NOT NULL,
			source_ip INET NOT NULL,
			source_port INTEGER,
			dest_ip INET NOT NULL,
			dest_port INTEGER,
			direction SMALLINT NOT NULL,
			bytes_sent BIGINT NOT NULL DEFAULT 0,
			bytes_received BIGINT NOT NULL DEFAULT 0,
			packets_sent BIGINT NOT NULL DEFAULT 0,
			packets_received BIGINT NOT NULL DEFAULT 0,
			started_at TIMESTAMP WITH TIME ZONE NOT NULL,
			ended_at TIMESTAMP WITH TIME ZONE,
			duration_seconds INTEGER,
			conntrack_id TEXT,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);

		-- Indexes for common query patterns
		CREATE INDEX IF NOT EXISTS idx_traffic_container_time
			ON traffic_connections(container_name, started_at DESC);
		CREATE INDEX IF NOT EXISTS idx_traffic_dest_ip
			ON traffic_connections(dest_ip);
		CREATE INDEX IF NOT EXISTS idx_traffic_dest_port
			ON traffic_connections(dest_port);
		CREATE INDEX IF NOT EXISTS idx_traffic_started_at
			ON traffic_connections(started_at DESC);

		-- Aggregated traffic stats table (for faster time-series queries)
		CREATE TABLE IF NOT EXISTS traffic_aggregates (
			id BIGSERIAL PRIMARY KEY,
			container_name TEXT NOT NULL,
			dest_ip INET,
			dest_port INTEGER,
			interval_start TIMESTAMP WITH TIME ZONE NOT NULL,
			interval_end TIMESTAMP WITH TIME ZONE NOT NULL,
			bytes_sent BIGINT NOT NULL DEFAULT 0,
			bytes_received BIGINT NOT NULL DEFAULT 0,
			connection_count INTEGER NOT NULL DEFAULT 0,
			UNIQUE(container_name, dest_ip, dest_port, interval_start)
		);

		CREATE INDEX IF NOT EXISTS idx_traffic_agg_container_time
			ON traffic_aggregates(container_name, interval_start DESC);
	`

	if _, err := s.pool.Exec(ctx, schema); err != nil {
		return err
	}

	// Migration (#1394): SaveConnection's `ON CONFLICT DO NOTHING` had
	// no conflict target to fire on — traffic_connections carried only
	// its auto-assigned BIGSERIAL primary key as a unique constraint,
	// and conntrack_id had a plain (non-unique) index. Every re-observed
	// flow (an LRU-evicted flow briefly reappearing, or the same flow
	// caught by both the eviction and closedFlows paths) landed as an
	// extra row, so history and its aggregates over-counted by however
	// many times a flow was re-observed.
	//
	// A unique index cannot be created while duplicates exist, so
	// dedup runs first: for each conntrack_id with more than one row,
	// keep the one with the largest byte total (the collector reports
	// cumulative counters, so the row that observed the flow longest
	// carries the most complete picture) and, on a tie, the earliest
	// (smallest id). Scoped to conntrack_id IS NOT NULL — Postgres
	// never considers two NULLs equal under a unique index, so rows
	// without a flow ID were never "duplicates" of one another and are
	// deliberately left untouched.
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM traffic_connections a
		USING traffic_connections b
		WHERE a.conntrack_id IS NOT NULL
			AND a.conntrack_id = b.conntrack_id
			AND (
				(a.bytes_sent + a.bytes_received) < (b.bytes_sent + b.bytes_received)
				OR (
					(a.bytes_sent + a.bytes_received) = (b.bytes_sent + b.bytes_received)
					AND a.id > b.id
				)
			)
	`); err != nil {
		return fmt.Errorf("dedup traffic_connections.conntrack_id before adding its unique index: %w", err)
	}

	// CREATE INDEX CONCURRENTLY cannot run inside a transaction block,
	// so it is issued on its own rather than folded into the schema
	// string above (pgx's simple query protocol runs a multi-statement
	// Exec as one implicit transaction).
	if _, err := s.pool.Exec(ctx,
		`CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_traffic_conntrack_id_unique
			ON traffic_connections(conntrack_id)`); err != nil {
		return fmt.Errorf("create unique index on traffic_connections.conntrack_id: %w", err)
	}

	return nil
}

// SaveConnection saves a completed connection to the database
func (s *Store) SaveConnection(ctx context.Context, conn *pb.Connection) error {
	query := `
		INSERT INTO traffic_connections (
			container_name, protocol, source_ip, source_port, dest_ip, dest_port,
			direction, bytes_sent, bytes_received, packets_sent, packets_received,
			started_at, ended_at, duration_seconds, conntrack_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT DO NOTHING
	`

	startedAt := conn.FirstSeen.AsTime()
	var endedAt *time.Time
	var durationSeconds *int64
	if conn.LastSeen != nil {
		t := conn.LastSeen.AsTime()
		endedAt = &t
		d := int64(t.Sub(startedAt).Seconds())
		durationSeconds = &d
	}

	_, err := s.pool.Exec(ctx, query,
		conn.ContainerName,
		safecast.I16(conn.Protocol),
		conn.SourceIp,
		conn.SourcePort,
		conn.DestIp,
		conn.DestPort,
		safecast.I16(conn.Direction),
		conn.BytesSent,
		conn.BytesReceived,
		conn.PacketsSent,
		conn.PacketsReceived,
		startedAt,
		endedAt,
		durationSeconds,
		conn.Id,
	)

	if err != nil {
		return fmt.Errorf("failed to save connection: %w", err)
	}

	return nil
}

// QueryParams holds parameters for querying traffic history
type QueryParams struct {
	// ContainerName scopes the query to one box. Empty means every box
	// (host-wide, #2884): callers must have authorized that before setting it.
	ContainerName string
	// StartTime and EndTime bound started_at, both inclusive.
	StartTime time.Time
	EndTime   time.Time
	DestIP    string
	DestPort  int
	Offset    int
	Limit     int
}

// OldestRecord returns started_at of the oldest connection held for a box (all
// boxes when containerName is empty), or nil when nothing is held. It lets a
// caller distinguish "no matching traffic" from "nothing collected" (#2884).
func (s *Store) OldestRecord(ctx context.Context, containerName string) (*time.Time, error) {
	var oldest *time.Time
	var err error
	if containerName == "" {
		err = s.pool.QueryRow(ctx, `SELECT MIN(started_at) FROM traffic_connections`).Scan(&oldest)
	} else {
		err = s.pool.QueryRow(ctx,
			`SELECT MIN(started_at) FROM traffic_connections WHERE container_name = $1`,
			containerName).Scan(&oldest)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read oldest record: %w", err)
	}
	return oldest, nil
}

// QueryConnections retrieves historical connections matching the criteria
func (s *Store) QueryConnections(ctx context.Context, params QueryParams) ([]*pb.HistoricalConnection, int32, error) {
	// Build query dynamically based on filters.
	//
	// source_ip and dest_ip go through host() (#1397). The columns are INET,
	// the scan targets are Go strings, and pgx v5 refuses to decode INET into
	// a string in binary format — so without this EVERY query that matched a
	// row failed with "cannot scan inet (OID 869) in binary format into
	// *string", and connection history was unreadable whenever it was
	// non-empty.
	//
	// host(), not ::text: a plain ::text cast renders an INET with its
	// netmask, so a single host address comes back as "93.184.216.34/32" and
	// the API hands clients an address that will not compare equal to the one
	// they stored. host() yields the bare address, for IPv4 and IPv6 alike.
	// Caught only because the test asserts the VALUE rather than merely that
	// the query returned without error.
	baseQuery := `
		SELECT id, container_name, protocol, host(source_ip), source_port, host(dest_ip), dest_port,
		       direction, bytes_sent, bytes_received, started_at, ended_at, duration_seconds
		FROM traffic_connections
		WHERE started_at >= $1 AND started_at <= $2
	`
	countQuery := `
		SELECT COUNT(*) FROM traffic_connections
		WHERE started_at >= $1 AND started_at <= $2
	`

	args := []interface{}{params.StartTime, params.EndTime}
	argIndex := 3
	if params.ContainerName != "" {
		baseQuery += fmt.Sprintf(" AND container_name = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND container_name = $%d", argIndex)
		args = append(args, params.ContainerName)
		argIndex++
	}

	if params.DestIP != "" {
		baseQuery += fmt.Sprintf(" AND dest_ip = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND dest_ip = $%d", argIndex)
		args = append(args, params.DestIP)
		argIndex++
	}

	if params.DestPort > 0 {
		baseQuery += fmt.Sprintf(" AND dest_port = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND dest_port = $%d", argIndex)
		args = append(args, params.DestPort)
		argIndex++
	}

	// Get total count
	var totalCount int32
	err := s.pool.QueryRow(ctx, countQuery, args...).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count connections: %w", err)
	}

	// Apply pagination
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	baseQuery += fmt.Sprintf(" ORDER BY started_at DESC LIMIT $%d OFFSET $%d", argIndex, argIndex+1)
	args = append(args, limit, params.Offset)

	rows, err := s.pool.Query(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query connections: %w", err)
	}
	defer rows.Close()

	var connections []*pb.HistoricalConnection
	for rows.Next() {
		var (
			id              int64
			containerName   string
			protocol        int16
			sourceIP        string
			sourcePort      *int32
			destIP          string
			destPort        *int32
			direction       int16
			bytesSent       int64
			bytesReceived   int64
			startedAt       time.Time
			endedAt         *time.Time
			durationSeconds *int64
		)

		err := rows.Scan(
			&id, &containerName, &protocol, &sourceIP, &sourcePort,
			&destIP, &destPort, &direction, &bytesSent, &bytesReceived,
			&startedAt, &endedAt, &durationSeconds,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan row: %w", err)
		}

		conn := &pb.HistoricalConnection{
			Id:            id,
			ContainerName: containerName,
			Protocol:      pb.Protocol(protocol),
			SourceIp:      sourceIP,
			DestIp:        destIP,
			Direction:     pb.TrafficDirection(direction),
			BytesSent:     bytesSent,
			BytesReceived: bytesReceived,
			StartedAt:     timestamppb.New(startedAt),
		}

		if sourcePort != nil {
			conn.SourcePort = safecast.U32(*sourcePort)
		}
		if destPort != nil {
			conn.DestPort = safecast.U32(*destPort)
		}
		if endedAt != nil {
			conn.EndedAt = timestamppb.New(*endedAt)
		}
		if durationSeconds != nil {
			conn.DurationSeconds = *durationSeconds
		}

		connections = append(connections, conn)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating rows: %w", err)
	}

	return connections, totalCount, nil
}

// AggregateParams holds parameters for querying traffic aggregates
type AggregateParams struct {
	ContainerName   string
	StartTime       time.Time
	EndTime         time.Time
	Interval        string
	GroupByDestIP   bool
	GroupByDestPort bool
}

// GetAggregates retrieves time-series traffic aggregates
func (s *Store) GetAggregates(ctx context.Context, params AggregateParams) ([]*pb.TrafficAggregate, error) {
	// Parse interval
	intervalDuration, err := parseInterval(params.Interval)
	if err != nil {
		return nil, fmt.Errorf("invalid interval: %w", err)
	}

	// Build the aggregation query
	selectCols := "date_trunc('hour', started_at) as bucket"
	groupCols := "date_trunc('hour', started_at)"

	if params.GroupByDestIP {
		// host() for the same reason as QueryConnections (#1397) — the column
		// is INET, the scan target is a Go string, and a bare ::text cast
		// would append the netmask. Grouped on the raw column so the
		// conversion cannot change how rows are bucketed.
		selectCols += ", host(dest_ip)"
		groupCols += ", dest_ip"
	}
	if params.GroupByDestPort {
		selectCols += ", dest_port"
		groupCols += ", dest_port"
	}

	query := fmt.Sprintf(`
		SELECT %s,
		       COALESCE(SUM(bytes_sent), 0) as bytes_sent,
		       COALESCE(SUM(bytes_received), 0) as bytes_received,
		       COUNT(*) as connection_count
		FROM traffic_connections
		WHERE container_name = $1 AND started_at >= $2 AND started_at <= $3
		GROUP BY %s
		ORDER BY bucket DESC
	`, selectCols, groupCols)

	rows, err := s.pool.Query(ctx, query, params.ContainerName, params.StartTime, params.EndTime)
	if err != nil {
		return nil, fmt.Errorf("failed to query aggregates: %w", err)
	}
	defer rows.Close()

	var aggregates []*pb.TrafficAggregate
	for rows.Next() {
		agg := &pb.TrafficAggregate{}

		var bucket time.Time
		var destIP *string
		var destPort *int32
		var bytesSent, bytesReceived int64
		var connCount int32

		// Scan based on grouping
		if params.GroupByDestIP && params.GroupByDestPort {
			err = rows.Scan(&bucket, &destIP, &destPort, &bytesSent, &bytesReceived, &connCount)
		} else if params.GroupByDestIP {
			err = rows.Scan(&bucket, &destIP, &bytesSent, &bytesReceived, &connCount)
		} else if params.GroupByDestPort {
			err = rows.Scan(&bucket, &destPort, &bytesSent, &bytesReceived, &connCount)
		} else {
			err = rows.Scan(&bucket, &bytesSent, &bytesReceived, &connCount)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to scan aggregate row: %w", err)
		}

		agg.Timestamp = timestamppb.New(bucket)
		agg.BytesSent = bytesSent
		agg.BytesReceived = bytesReceived
		agg.ConnectionCount = connCount

		if destIP != nil {
			agg.DestIp = *destIP
		}
		if destPort != nil {
			agg.DestPort = safecast.U32(*destPort)
		}

		aggregates = append(aggregates, agg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating aggregate rows: %w", err)
	}

	// Re-aggregate to the requested interval if needed
	if intervalDuration > time.Hour {
		aggregates = reAggregate(aggregates, intervalDuration)
	}

	return aggregates, nil
