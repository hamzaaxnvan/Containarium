package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
)

// TestCookieSession_SetClear verifies the POST/DELETE
// /v1/auth/session endpoint contract for issue #338:
//   - POST with valid bearer mints a cookie carrying that JWT
//   - The cookie is HttpOnly + SameSite=Lax
//   - The cookie's Max-Age tracks the JWT's remaining lifetime
//   - DELETE clears the cookie (Max-Age=-1)
//   - Missing / junk bearer returns 401 and sets no cookie
//   - Wrong method returns 405 with an Allow header
func TestCookieSession_SetClear(t *testing.T) {
	tm, err := auth.NewTokenManager("test-secret-key-for-cookie-session-handler", "test-issuer")
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	mw := auth.NewAuthMiddleware(tm)

	mux := http.NewServeMux()
	registerCookieSession(mux, mw)

	t.Run("POST without bearer → 401, no cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/session", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if c := rec.Result().Cookies(); len(c) != 0 {
			t.Errorf("expected no cookies, got %v", c)
		}
	})
