package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/footprintai/containarium/internal/audit"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Models storage idempotency only. This is not evidence of a Postgres
// transaction, unique constraint, or hash-chain integration passing.
type authorizationMemoryLogger struct {
	seen map[string]bool
	rows []audit.BatchEntry
}

func (f *authorizationMemoryLogger) LogBatch(_ context.Context, entries []audit.BatchEntry) ([]audit.BatchOutcome, error) {
	if f.seen == nil {
		f.seen = make(map[string]bool)
	}
	outcomes := make([]audit.BatchOutcome, len(entries))
