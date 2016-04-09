package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// bodyRecorder serves a canned 200 and captures the raw request body, so
// a test can assert what actually went on the wire rather than what the
// caller believed it sent.
type bodyRecorder struct {
	raw      []byte
	path     string
	method   string
	response string
}

func newBodyRecorder(t *testing.T, response string) (*bodyRecorder, *HTTPClient) {
	t.Helper()
	rec := &bodyRecorder{response: response}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.raw, _ = io.ReadAll(r.Body)
		rec.path = r.URL.Path
		rec.method = r.Method
		w.Header().Set("Content-Type", "application/json")
		if rec.response != "" {
			_, _ = w.Write([]byte(rec.response))
		}
	}))
	t.Cleanup(srv.Close)

	c, err := NewHTTPClient(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	return rec, c
}
