package sentinel

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// AuditIngestTokenRegisterRequest is the JSON body POSTed to
// AuditIngestTokenRegisterHandler.
type AuditIngestTokenRegisterRequest struct {
	// BackendID is the sentinel's id for the backend (the same id keysync
	// and the backend pool use).
	BackendID string `json:"backend_id"`
	// Token is a JWT minted ON that backend with the audit:ingest scope.
	Token string `json:"token"`
}

// AuditIngestTokenDeregisterRequest is the JSON body sent (DELETE) to
// AuditIngestTokenDeregisterHandler.
type AuditIngestTokenDeregisterRequest struct {
	BackendID string `json:"backend_id"`
}

// SetAuditIngestTokenStorePath overrides where audit-ingest tokens are
// persisted. Tests point it at a tmp dir; production leaves it unset and
// uses DefaultAuditIngestTokenStorePath.
func (m *Manager) SetAuditIngestTokenStorePath(path string) {
	m.auditIngestTokenStorePath = path
