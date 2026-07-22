package incus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Phase 3.1 Phase-A — simplestreams resolver tests.
//
// We stand up a httptest server that serves a canned
// images.json. The resolver fetches from it under the
// same path the production simplestreams remotes use, so
// the only difference between test and real is the
// hostname.

// fakeIndex builds a minimal-but-realistic simplestreams
// products:1.0 index with one product (`ubuntu/24.04`)
// having two versions, each with three items.
func fakeIndex() imagesIndex {
	return imagesIndex{
		Format: "products:1.0",
		Products: map[string]productEntry{
			"ubuntu:24.04:amd64:default": {
				Aliases: "24.04,noble,ubuntu/24.04",
				Arch:    "amd64",
				Versions: map[string]versionEntry{
					"20240519_07:42": {
						Items: map[string]itemEntry{
							"lxd.tar.xz": {
								FType:  "lxd.tar.xz",
								SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
								Size:   12345,
							},
							"root.squashfs": {
								FType:  "squashfs",
								SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
								Size:   67890,
							},
							"lxd_combined.tar.gz": {
								FType:  "lxd_combined.tar.gz",
								SHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
								Size:   98765,
							},
						},
					},
					"20240520_07:42": {
						Items: map[string]itemEntry{
							"lxd.tar.xz": {
								FType:  "lxd.tar.xz",
								SHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
								Size:   12345,
							},
						},
					},
				},
			},
			"alpine:3.19:amd64:default": {
				Aliases: "3.19,alpine/3.19",
				Arch:    "amd64",
				Versions: map[string]versionEntry{
					"20240301_13:00": {
						Items: map[string]itemEntry{
							"root.squashfs": {
								FType:  "squashfs",
								SHA256: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
								Size:   45678,
							},
						},
					},
				},
			},
		},
	}
}

// newFakeStreamsServer serves the fakeIndex JSON at the
// canonical simplestreams path. Returns the test server
// and a *bool the test can flip to make the next request
// 500.
func newFakeStreamsServer(t *testing.T) (*httptest.Server, *bool) {
	t.Helper()
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != indexPath {
			http.NotFound(w, r)
			return
		}
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fakeIndex())
	}))
	return srv, &fail
}

// --- Tests ---

func TestResolveImageDigests_HappyPath(t *testing.T) {
	srv, _ := newFakeStreamsServer(t)
	defer srv.Close()

	r := NewStreamsResolver(0)
	got, err := r.ResolveImageDigests(context.Background(), srv.URL, "ubuntu/24.04")
	if err != nil {
		t.Fatalf("ResolveImageDigests: %v", err)
	}
	// Expect all 4 distinct digests across both versions
	// of the ubuntu product. Order is non-deterministic
	// because we walk a map; assert on set membership.
	if len(got) != 4 {
		t.Fatalf("got %d digests, want 4: %v", len(got), got)
	}
	want := []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
