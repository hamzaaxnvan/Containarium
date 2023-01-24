package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func anonTestClient(t *testing.T, h http.HandlerFunc) *HTTPClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewHTTPClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The HTTP client speaks protojson both ways on the REST shim and reports
// the daemon's error text verbatim; a 404 is "service not exposed".
func TestHTTPClient_EnsureAnonymousBox(t *testing.T) {
	var gotPath, gotBody string
	c := anonTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = w.Write([]byte(`{"boxName":"anon-1a2b3c4d-container","sshHost":"10.0.0.9","sshPort":22,"sshUser":"anon-1a2b3c4d","ttlExpiresAt":"2026-10-01T16:00:00Z","reused":true}`))
	})
