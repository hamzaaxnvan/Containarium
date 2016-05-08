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
