package containariumotel

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPMiddleware_PassesThroughToHandler(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	})

	mw := HTTPMiddleware(next)
	if mw == nil {
		t.Fatal("HTTPMiddleware returned nil")
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	mw.ServeHTTP(rr, req)

