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

	t.Run("POST with junk bearer → 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/session", nil)
		req.Header.Set("Authorization", "Bearer not.a.real.jwt")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST with valid bearer → 200, HttpOnly cookie minted", func(t *testing.T) {
		token, err := tm.GenerateAccessToken("test-user", []string{"admin"}, 10*time.Minute)
		if err != nil {
			t.Fatalf("GenerateAccessToken: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/v1/auth/session", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		cookies := rec.Result().Cookies()
		var c *http.Cookie
		for _, ck := range cookies {
			if ck.Name == auth.SessionCookieName {
				c = ck
				break
			}
		}
		if c == nil {
			t.Fatalf("expected %q cookie to be set, got %v", auth.SessionCookieName, cookies)
		}
		if c.Value != token {
			t.Errorf("cookie value mismatch: cookie is not the bearer JWT")
		}
		if !c.HttpOnly {
			t.Errorf("cookie must be HttpOnly to defend against XSS reading the JWT")
		}
