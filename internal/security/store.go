package security

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Store handles persistent storage of ClamAV scan reports
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new security store connected to PostgreSQL
func NewStore(ctx context.Context, pool *pgxpool.Pool) (*Store, error) {
	store := &Store{pool: pool}

	if err := store.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize security schema: %w", err)
	}

	return store, nil
}

// initSchema creates the database schema if it doesn't exist
func (s *Store) initSchema(ctx context.Context) error {
	schema := `
		CREATE TABLE IF NOT EXISTS clamav_reports (
			id BIGSERIAL PRIMARY KEY,
			container_name TEXT NOT NULL,
			username TEXT NOT NULL,
			status TEXT NOT NULL,
			findings_count INTEGER NOT NULL DEFAULT 0,
			findings TEXT NOT NULL DEFAULT '',
			scanned_at TIMESTAMP WITH TIME ZONE NOT NULL,
			scan_duration TEXT,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			UNIQUE(container_name, scanned_at)
		);

		CREATE INDEX IF NOT EXISTS idx_clamav_container_time
			ON clamav_reports(container_name, scanned_at DESC);
		CREATE INDEX IF NOT EXISTS idx_clamav_status
			ON clamav_reports(status);
		CREATE INDEX IF NOT EXISTS idx_clamav_scanned_at
			ON clamav_reports(scanned_at DESC);

		CREATE TABLE IF NOT EXISTS scan_jobs (
			id BIGSERIAL PRIMARY KEY,
			container_name TEXT NOT NULL,
			username TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			retry_count INTEGER NOT NULL DEFAULT 0,
			max_retries INTEGER NOT NULL DEFAULT 2,
			error_message TEXT,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			started_at TIMESTAMP WITH TIME ZONE,
			completed_at TIMESTAMP WITH TIME ZONE
		);
		CREATE INDEX IF NOT EXISTS idx_scan_jobs_status ON scan_jobs(status);
		CREATE INDEX IF NOT EXISTS idx_scan_jobs_created_at ON scan_jobs(created_at DESC);
	`

	_, err := s.pool.Exec(ctx, schema)
	return err
}

// Report represents a ClamAV scan result
type Report struct {
	ContainerName string
	Username      string
	Status        string // ScanStatusClean | ScanStatusInfected | ScanStatusIncomplete
	FindingsCount int
	Findings      string
	ScannedAt     time.Time
	ScanDuration  string
}

// SaveReport saves a scan report to the database
func (s *Store) SaveReport(ctx context.Context, report *Report) error {
	query := `
		INSERT INTO clamav_reports (
			container_name, username, status, findings_count, findings,
			scanned_at, scan_duration
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (container_name, scanned_at) DO NOTHING
	`

	_, err := s.pool.Exec(ctx, query,
		report.ContainerName,
		report.Username,
		report.Status,
		report.FindingsCount,
		report.Findings,
		report.ScannedAt,
		report.ScanDuration,
	)
	if err != nil {
		return fmt.Errorf("failed to save report: %w", err)
	}

	return nil
}

// ListParams holds parameters for listing reports
type ListParams struct {
	ContainerName string
	Status        string
	From          string // ISO date
	To            string // ISO date
	Limit         int
	Offset        int
}

// ListReports retrieves scan reports with optional filtering
func (s *Store) ListReports(ctx context.Context, params ListParams) ([]*pb.ClamavReport, int32, error) {
	baseQuery := `SELECT id, container_name, username, status, findings_count, findings,
		scanned_at, scan_duration, created_at FROM clamav_reports WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM clamav_reports WHERE 1=1`

	var args []interface{}
	argIdx := 1

	if params.ContainerName != "" {
		baseQuery += fmt.Sprintf(" AND container_name = $%d", argIdx)
		countQuery += fmt.Sprintf(" AND container_name = $%d", argIdx)
		args = append(args, params.ContainerName)
		argIdx++
	}

	if params.Status != "" {
		baseQuery += fmt.Sprintf(" AND status = $%d", argIdx)
		countQuery += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, params.Status)
		argIdx++
	}

	if params.From != "" {
		baseQuery += fmt.Sprintf(" AND scanned_at >= $%d", argIdx)
		countQuery += fmt.Sprintf(" AND scanned_at >= $%d", argIdx)
		args = append(args, params.From)
		argIdx++
	}
