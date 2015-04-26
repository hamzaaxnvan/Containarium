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
