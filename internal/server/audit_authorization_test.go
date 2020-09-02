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
	for i, entry := range entries {
		if f.seen[entry.DedupeKey] {
			outcomes[i] = audit.BatchDuplicate
		} else {
			f.seen[entry.DedupeKey] = true
			f.rows = append(f.rows, entry)
			outcomes[i] = audit.BatchInserted
		}
	}
	return outcomes, nil
}

func TestIngest_AuthorizationReasonsAndReplay(t *testing.T) {
	for number, name := range pb.SSHDenyReason_name {
		t.Run(name, func(t *testing.T) {
			denied := openRec("authorization-session")
			denied.Phase = pb.SSHSessionPhase_SSH_SESSION_PHASE_DENIED
			denied.UserId = "user-test"
			denied.AuthzDecision = pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_DENY
			denied.DenyReason = pb.SSHDenyReason(number)
			denied.Enforced = false // audit-only: a later OPEN is a separate row.
			opened := openRec(denied.SessionId)
			opened.UserId, opened.AuthzDecision, opened.DenyReason = denied.UserId, denied.AuthzDecision, denied.DenyReason
