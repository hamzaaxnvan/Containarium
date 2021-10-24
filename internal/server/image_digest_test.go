package server

import (
	"strings"
	"sync"
	"testing"
)

// Phase 3.1 follow-up — image-digest-pinning tests.

// resetDigestOnce flips imageDigestOnce back to its zero
// state so each test can drive a fresh env-var read. Tests
// run sequentially via t.Setenv inside this package, so
// no concurrent-init race.
func resetDigestOnce(t *testing.T) {
	t.Helper()
	imageDigestOnce = sync.Once{}
	imageDigestReq = false
}

func TestExtractImageDigest(t *testing.T) {
	cases := map[string]struct {
		want   string
		wantOK bool
	}{
		"ubuntu:22.04@sha256:abc":             {"sha256:abc", true},
		"ubuntu:22.04":                        {"", false},
		"":                                    {"", false},
		"images:ubuntu/22.04@sha256:deadbeef": {"sha256:deadbeef", true},
		// Multi-@ is unusual but the last `@` wins (matches
		// OCI reference semantics for digest pinning).
		"a@b@sha256:xyz": {"sha256:xyz", true},
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got, ok := extractImageDigest(in)
			if got != want.want || ok != want.wantOK {
				t.Fatalf("extractImageDigest(%q) = (%q,%v); want (%q,%v)",
					in, got, ok, want.want, want.wantOK)
			}
		})
	}
}

// Valid digest passes regardless of env value (the env
// only gates the requirement; never the format).
func TestValidateImageDigest_DisabledByDefault(t *testing.T) {
	resetDigestOnce(t)
	t.Setenv(requireImageDigestEnv, "")
	// No digest at all — accepted because enforcement off.
	if err := validateImageDigest("ubuntu:22.04"); err != nil {
		t.Fatalf("unenforced: should pass; got %v", err)
	}
}

func TestValidateImageDigest_EnforcedRejectsBareTag(t *testing.T) {
	resetDigestOnce(t)
	t.Setenv(requireImageDigestEnv, "true")

	err := validateImageDigest("ubuntu:22.04")
	if err == nil {
		t.Fatal("bare tag should be rejected when enforcement is on")
	}
	if !strings.Contains(err.Error(), "missing a digest") {
		t.Fatalf("error should mention missing digest; got %v", err)
	}
}

func TestValidateImageDigest_EnforcedAcceptsValidDigest(t *testing.T) {
	resetDigestOnce(t)
