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
		t.Error("first call should not be from cache")
	}
	if rel.TagName != "v0.23.0" {
		t.Errorf("tag = %q", rel.TagName)
	}

	// Second call within TTL → cache hit, no extra upstream request.
	_, cached2, err := c.Latest(context.Background())
	if err != nil || !cached2 {
		t.Fatalf("second Latest: cached=%v err=%v", cached2, err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected 1 upstream hit, got %d", got)
	}
}

func TestLatest_RefetchesAfterTTL(t *testing.T) {
	var hits int32
	srv := fakeGitHub(t, "v0.23.0", &hits)
	now := time.Unix(1_000_000, 0)
	c := NewClient(WithURL(srv.URL), WithTTL(time.Hour), WithClock(func() time.Time { return now }))

	if _, _, err := c.Latest(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour) // advance past TTL
	if _, cached, err := c.Latest(context.Background()); err != nil || cached {
		t.Fatalf("post-TTL call should refetch: cached=%v err=%v", cached, err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("expected 2 upstream hits, got %d", got)
	}
}

func TestLatest_ServesStaleOnError(t *testing.T) {
	var hits int32
	flaky := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if atomic.AddInt32(&flaky, 1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"tag_name":"v0.23.0"}`)
			return
		}
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	now := time.Unix(1_000_000, 0)
	c := NewClient(WithURL(srv.URL), WithTTL(time.Minute), WithClock(func() time.Time { return now }))

	if _, _, err := c.Latest(context.Background()); err != nil {
		t.Fatalf("seed fetch: %v", err)
	}
	now = now.Add(2 * time.Minute) // expire cache; next fetch errors
	rel, cached, err := c.Latest(context.Background())
	if err != nil {
		t.Fatalf("should serve stale on error, got err %v", err)
	}
	if !cached || rel.TagName != "v0.23.0" {
		t.Errorf("expected stale cached v0.23.0, got cached=%v rel=%+v", cached, rel)
	}
}

func TestLatest_ErrorWhenNoCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
