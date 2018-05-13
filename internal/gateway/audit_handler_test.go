package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
)

// Phase 1.5 — audit endpoint must require Authorization: Bearer
// and reject the legacy `?token=...` query-string form (audit
// finding A-MED-3). Query strings get logged by every reverse
// proxy in the request path, which silently re-leaked the admin
// token to anyone with log access.

const auditTestSecret = "a-secret-that-is-at-least-32-bytes-long!!"

func newAuditTestMux(t *testing.T) (*http.ServeMux, *auth.AuthMiddleware, string) {
	t.Helper()
	tm, err := auth.NewTokenManager(auditTestSecret, "containarium")
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	tok, err := tm.GenerateToken("alice", []string{"admin"}, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	mw := auth.NewAuthMiddleware(tm)

	mux := http.NewServeMux()
	// nil store is acceptable: the auth check runs BEFORE we
	// touch the store, so an unauthenticated request returns 401
	// and never reaches handleAuditQuery. The authenticated path
	// would NPE without a real store, which we don't exercise.
	registerAuditEndpoint(mux, nil, mw)
	return mux, mw, tok
}

func TestAuditEndpoint_RejectsMissingAuth(t *testing.T) {
	mux, _, _ := newAuditTestMux(t)
	rec := httptest.NewRecorder()
