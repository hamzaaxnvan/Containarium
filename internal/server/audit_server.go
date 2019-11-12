package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// maxIngestBatch caps one IngestSSHSessionRecords call. The batch holds the
// audit chain's advisory lock for its whole transaction, so the cap bounds
// how long other appenders can be made to wait.
const maxIngestBatch = 500

// Audit actions written for sentinel SSH session records (#2415). Named to
// match the existing in-box collector's "ssh_login".
const (
	auditActionSSHSessionOpen   = "ssh_session_open"
	auditActionSSHSessionClose  = "ssh_session_close"
	auditActionSSHSessionDenied = "ssh_session_denied"
	// Source-IP records from the sentinel's ssh-ban-plugin (#2419).
	auditActionSSHAuthFailed = "ssh_auth_failed"
	auditActionSSHBanned     = "ssh_banned"
	auditResourceSSHSession  = "ssh_session"
)

// batchLogger is the slice of *audit.Store the ingest handler needs,
// narrowed so the handler is testable without Postgres.
type batchLogger interface {
	LogBatch(ctx context.Context, entries []audit.BatchEntry) ([]audit.BatchOutcome, error)
}

// AuditServer implements AuditService (#2415).
type AuditServer struct {
	pb.UnimplementedAuditServiceServer
	store batchLogger
}

// NewAuditServer returns the AuditService backed by store. A nil store (the
// daemon has no Postgres) makes every RPC return Unavailable.
func NewAuditServer(store batchLogger) *AuditServer {
	return &AuditServer{store: store}
}

// SSHSessionDetail is the typed JSON stored in an ssh_session row's detail
// column. A named struct, not a map, so the shape is a contract the
// auditor-facing docs and tests can name.
type SSHSessionDetail struct {
	Target           string              `json:"target,omitempty"`
	AuthMethod       string              `json:"auth_method,omitempty"`
	ClientPort       int                 `json:"client_port,omitempty"`
	KeyID            string              `json:"key_id,omitempty"`
	Serial           uint64              `json:"serial,omitempty"`
	CAKeyFingerprint string              `json:"ca_key_fingerprint,omitempty"`
	KeyFingerprint   string              `json:"key_fingerprint,omitempty"`
	CloseReason      string              `json:"close_reason,omitempty"`
	SentinelID       string              `json:"sentinel_id,omitempty"`
	UserID           string              `json:"user_id,omitempty"`
	AuthzDecision    pb.SSHAuthzDecision `json:"authz_decision,omitempty"`
