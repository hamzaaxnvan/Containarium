package releases

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func fakeGitHub(t *testing.T, tag string, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"tag_name":%q,"name":"Release %s","html_url":"https://github.com/x/releases/%s","published_at":"2026-06-01T00:00:00Z"}`, tag, tag, tag)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLatest_CachesWithinTTL(t *testing.T) {
	var hits int32
	srv := fakeGitHub(t, "v0.23.0", &hits)
	c := NewClient(WithURL(srv.URL), WithTTL(time.Hour))

	rel, cached, err := c.Latest(context.Background())
	if err != nil {
		t.Fatalf("first Latest: %v", err)
	}
	if cached {
