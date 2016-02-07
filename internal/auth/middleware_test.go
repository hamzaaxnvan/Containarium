package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHTTPMiddleware_AuthGate is the regression test for the
// /v1/backends auth fix and any future endpoint that wraps a handler
// with HTTPMiddleware. Proves the gate rejects missing / malformed /
// invalid tokens, and lets through valid ones.
//
// Why this test lives here and not in internal/gateway: the wrapping
// is one line in the gateway, and what's actually being verified is
// the middleware's contract. A handler wrapped with HTTPMiddleware
// MUST reject anonymous requests — every endpoint that relies on this
// (containers, backends, audit logs, …) inherits the same protection.
func TestHTTPMiddleware_AuthGate(t *testing.T) {
	tm, _ := NewTokenManager("test-secret-key-for-auth-middleware-test", "test-issuer")
	mw := NewAuthMiddleware(tm)

	// Stub handler that records whether it was called. If the auth
	// middleware lets a request through unauthenticated, this flips
	// true and the test fails.
	called := false
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok": true}`))
	})
	wrapped := mw.HTTPMiddleware(stub)

	cases := []struct {
		name     string
		setup    func(r *http.Request)
