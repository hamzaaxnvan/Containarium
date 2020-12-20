package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
)

// Phase 5.1 — /swagger-ui and /swagger.json must require
// the admin role. Exposing the full API surface to any
// authenticated caller (let alone unauthenticated) is the
// audit finding A-LOW-1; the gate closes it.
//
// We can't trivially exercise the full mux setup here, so
// the test targets the requireAdminFromContext helper in
// isolation. The auth-middleware step in front of it is
// exercised by middleware_test.go in internal/auth.

func TestRequireAdminFromContext_AllowsAdmin(t *testing.T) {
	called := false
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	wrapped := requireAdminFromContext(stub)

	req := httptest.NewRequest("GET", "/swagger.json", nil)
	ctx := auth.ContextWithClaims(req.Context(), &auth.Claims{
		Username: "ops",
		Roles:    []string{auth.RoleAdmin},
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

