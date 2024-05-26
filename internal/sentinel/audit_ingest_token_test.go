package sentinel

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
)

// #2415 — audit-ingest token store + register/deregister handlers.

const auditIngestAdminSecret = "zyxwvutsrqponmlkjihgfedcba9876543210ZYXW"

func newManagerForAuditIngestTest(t *testing.T) (*Manager, string) {
	t.Helper()
	m := &Manager{backends: NewBackendPool()}
	m.SetAdminSecret([]byte(auditIngestAdminSecret))
	path := filepath.Join(t.TempDir(), "audit-ingest-tokens.json")
	m.SetAuditIngestTokenStorePath(path)
	return m, path
}

func signedReq(method string, body any, secret string) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "/sentinel/audit-ingest-tokens", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		auth.SignSentinelRequest(req, []byte(secret))
	}
	return req
}

func serveRegister(m *Manager, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	auth.SentinelHMACMiddleware(m.adminSecret, m.AuditIngestTokenRegisterHandler()).ServeHTTP(rec, req)
	return rec
}

func serveDeregister(m *Manager, req *http.Request) *httptest.ResponseRecorder {
