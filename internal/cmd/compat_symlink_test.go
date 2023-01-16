//go:build !windows && !containarium_client

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureCompatSymlink_NoOpWhenNewBinaryAbsent pins the safety property:
// service install / pool join must not touch /usr/local/bin/containarium at
// all until containariumd actually exists — replacing a still-in-use binary
// with a symlink to nothing would brick the host instead of converging it.
func TestEnsureCompatSymlink_NoOpWhenNewBinaryAbsent(t *testing.T) {
	root := t.TempDir()
	if err := ensureCompatSymlink(root); err != nil {
		t.Fatalf("ensureCompatSymlink: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, compatSymlinkOldPath)); !os.IsNotExist(err) {
		t.Errorf("expected no file at the old path, got err=%v", err)
	}
}

// TestEnsureCompatSymlink_ReplacesOldRealBinary is the Phase 1 rollout case:
// a host still has the real (pre-#1780) binary at the old path; once
