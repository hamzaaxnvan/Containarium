package secrets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	corecrypto "github.com/footprintai/containarium/pkg/core/secrets"
)

// Phase 4.1 Phase-D — legacy → envelope migration (audit
// C-HIGH-6). Rewrites pre-KMS rows (wrapped_dek IS NULL)
// through the envelope path so the entire deployment runs
// on KMS-mediated decryption. Operator-driven: typically
// scheduled after the daemon is rolled with WithKMS
// enabled, and run repeatedly until VerifyEnvelopeCoverage
// reports 100%.
//
// Properties:
//
//   - Idempotent: rows already in envelope form
//     (wrapped_dek IS NOT NULL) are skipped. Re-running
//     the migration after a partial run resumes where it
//     left off — there's no checkpoint to track.
//   - Atomic per row: each row is rewritten in a single
//     UPDATE; failures don't leave the row half-migrated.
//   - Verifies before committing: the new envelope form
//     is decrypted in a separate operation and byte-
//     compared to the original plaintext. A mismatch
//     ROLLS BACK the row's UPDATE and is reported as an
//     error in the result — never quietly accepted.
//   - Bounded memory: pages through rows in batches so
//     large deployments don't OOM.

// MigrateOptions controls the migration's pacing.
type MigrateOptions struct {
	// BatchSize is the number of rows scanned per page.
	// Lower → smaller transactions, less memory; higher →
	// fewer round-trips. Default 100.
	BatchSize int

	// MaxRows caps the total rows processed in one call.
	// 0 = unlimited. Useful for chunking a huge backlog
	// across maintenance windows.
	MaxRows int

	// DryRun, when true, walks the rows and verifies each
	// would round-trip cleanly but does NOT issue any
	// UPDATE. Use to confirm the migration is safe before
	// committing.
	DryRun bool
}

// MigrateResult summarizes what the migration did.
type MigrateResult struct {
	Scanned     int // rows we looked at
	Migrated    int // rows successfully rewritten to envelope
	AlreadyDone int // rows already in envelope form (skipped)
	Failed      int // rows that errored mid-migration
	StartedAt   time.Time
	CompletedAt time.Time
	Errors      []MigrationError
}

// MigrationError names a row that failed and why.
type MigrationError struct {
	Username string
	Name     string
	Err      string
}

// ErrMigrateNoKMS is returned when MigrateLegacyToEnvelope
// is called on a Store without a KMSClient — there's
// nothing to migrate TO.
var ErrMigrateNoKMS = errors.New("secrets: cannot migrate to envelope without WithKMS")

// MigrateLegacyToEnvelope rewrites legacy rows in batches.
// See package-level doc for properties + safety contract.
func (s *Store) MigrateLegacyToEnvelope(ctx context.Context, opts MigrateOptions) (MigrateResult, error) {
	if s.kms == nil {
		return MigrateResult{}, ErrMigrateNoKMS
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}

	res := MigrateResult{StartedAt: time.Now()}
	defer func() { res.CompletedAt = time.Now() }()

	for opts.MaxRows <= 0 || res.Scanned < opts.MaxRows {
		batchLimit := opts.BatchSize
		if opts.MaxRows > 0 {
			remaining := opts.MaxRows - res.Scanned
			if remaining < batchLimit {
				batchLimit = remaining
			}
		}

		rows, err := s.fetchLegacyBatch(ctx, batchLimit)
		if err != nil {
			return res, fmt.Errorf("fetch legacy batch: %w", err)
		}
		if len(rows) == 0 {
			break // no more legacy rows
		}

		for _, r := range rows {
			res.Scanned++
			migrateErr := s.migrateOne(ctx, r, opts.DryRun)
			switch {
			case migrateErr == nil:
				res.Migrated++
			case errors.Is(migrateErr, errAlreadyEnvelope):
				res.AlreadyDone++
			default:
				res.Failed++
				res.Errors = append(res.Errors, MigrationError{
					Username: r.username, Name: r.name, Err: migrateErr.Error(),
				})
				log.Printf("[secrets-migrate] %s/%s failed: %v", r.username, r.name, migrateErr)
			}
