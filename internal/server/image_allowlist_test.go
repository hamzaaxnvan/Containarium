package server

import (
	"strings"
	"sync"
	"testing"
)

// Phase 3.1 — image-registry allowlist (audit B-HIGH-1).
//
// loadImageAllowlist caches its result via sync.Once, so each test
// case operates on a freshly-allocated allowlist by resetting the
// package-level state. (The production code reads the env var
// once per process — these tests just verify the parsing +
// matching logic.)

func resetImageAllowlist(t *testing.T) {
	t.Helper()
	imageAllowlist = nil
	imageAllowlistAll = false
	// Replace the Once so it'll re-fire on next call.
	imageAllowlistOnce = sync.Once{}
}

func TestImageRegistryPrefix(t *testing.T) {
	cases := map[string]string{
		"ubuntu:22.04":                     "ubuntu",
		"images:ubuntu/22.04/cloud":        "images",
		"incus:noble":                      "incus",
		"https://images.example.com/x.img": "https://images.example.com",
		"http://internal/foo":              "http://internal",
		"ubuntu":                           "ubuntu", // bare name → default remote
		"22.04":                            "ubuntu",
		"images/ubuntu/22.04":              "", // path without remote → no prefix → rejected
		"":                                 "",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got := imageRegistryPrefix(in)
			if got != want {
				t.Errorf("prefix(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestValidateImageRegistry_EmptyAllowlistAcceptsAll(t *testing.T) {
	t.Setenv(allowedImageRegistriesEnv, "")
	resetImageAllowlist(t)

	cases := []string{"ubuntu:22.04", "https://random.example", "anything"}
	for _, in := range cases {
		if err := validateImageRegistry(in); err != nil {
			t.Errorf("unset allowlist must accept %q: %v", in, err)
		}
