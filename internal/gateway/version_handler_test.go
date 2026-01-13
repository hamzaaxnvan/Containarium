package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/releases"
	"github.com/footprintai/containarium/pkg/version"
)

// fakeGitHubLatest serves a minimal GitHub "latest release" response.
func fakeGitHubLatest(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"tag_name":%q,"name":"Release %s","html_url":"https://github.com/x/releases/%s","published_at":"2026-06-01T00:00:00Z"}`, tag, tag, tag)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newVersionMux(t *testing.T, tag string) (*http.ServeMux, string) {
	t.Helper()
	tm, err := auth.NewTokenManager("test-secret-key-for-version-handler-test", "test-issuer")
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	tok, err := tm.GenerateToken("alice", []string{"admin"}, 0)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	gh := fakeGitHubLatest(t, tag)
	rc := releases.NewClient(releases.WithURL(gh.URL))
	mux := http.NewServeMux()
	registerVersionEndpoints(mux, rc, auth.NewAuthMiddleware(tm))
	return mux, tok
}

func TestVersionEndpoint_RequiresAuth(t *testing.T) {
	mux, _ := newVersionMux(t, "v99.0.0")
	req := httptest.NewRequest(http.MethodGet, "/v1/releases/latest", nil) // no Bearer
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
