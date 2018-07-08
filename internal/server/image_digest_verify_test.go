package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Phase 3.1 Phase-B — verification-gate tests.
//
// The Phase A resolver is tested in pkg/core/incus.
// These tests cover the gate's policy layer:
//   - env-flag opt-in (default off)
//   - server-URL prefix mapping
//   - skip semantics for unresolvable / local aliases
//   - the actual reject/pass decision for matching and
//     non-matching digests

// resetVerifyState is called by each test that flips
// CONTAINARIUM_VERIFY_IMAGE_DIGEST. The gate uses
// sync.Once so cached state persists across tests in the
// same process; clearing the cached state by reassigning
// the Once + bool is the cleanest way to make each test
// independent.
func resetVerifyState() {
	verifyDigestOnce = sync.Once{}
	verifyDigestOn = false
	verifyResolverOnce = sync.Once{}
	verifyResolver = nil
	// Also reset the REQUIRE-gate's once so its log
	// behavior is reproducible per-test. Tests that
	// flip REQUIRE on/off would otherwise see the cached
	// state from a previous test in the same process.
	imageDigestOnce = sync.Once{}
	imageDigestReq = false
}

// captureLog redirects the std log to a buffer for the
// duration of `fn`, then restores. Returns the captured
// output. Used to assert WARNING lines from the verify
// gate's startup banner.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevWriter := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	}()
	fn()
	return buf.String()
}

// fakeIndexJSON mirrors the test fixture in pkg/core/incus
// at a lower fidelity — one product, three items, two
// versions. The exact structure matches what
// images.linuxcontainers.org would publish.
const fakeIndexJSON = `{
  "format": "products:1.0",
  "products": {
    "ubuntu:24.04:amd64:default": {
      "aliases": "24.04,noble,ubuntu/24.04",
      "arch": "amd64",
      "versions": {
        "20240519_07:42": {
          "items": {
            "lxd.tar.xz": {"ftype": "lxd.tar.xz", "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "size": 1},
            "root.squashfs": {"ftype": "squashfs", "sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "size": 1}
          }
        }
      }
    }
  }
}`

// newFakeIndexServer serves the canned index at the
// canonical simplestreams path.
func newFakeIndexServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/streams/v1/images.json" {
			http.NotFound(w, r)
			return
		}
		// Validate the JSON parses before serving — guard
		// against accidental edits breaking every test in
		// this file silently.
		var probe map[string]any
		if err := json.Unmarshal([]byte(fakeIndexJSON), &probe); err != nil {
			t.Fatalf("test fixture JSON is malformed: %v", err)
		}
		_, _ = w.Write([]byte(fakeIndexJSON))
	}))
}

// --- env flag tests ---

func TestVerifyDigest_DefaultIsOff(t *testing.T) {
	resetVerifyState()
	t.Setenv(verifyImageDigestEnv, "")
	// Even with a nonsense image, verification is OFF →
	// nil error.
	err := verifyImageDigestAgainstRegistry(context.Background(),
		"images:ubuntu/24.04@sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("verification should be OFF by default; got %v", err)
	}
}
