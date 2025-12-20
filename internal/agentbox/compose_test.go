//go:build !windows

package agentbox

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Tests focus on the pure-Go pieces — filesystem walk, slug
// derivation, compose-ps JSON parsing, arg helpers. The
// systemd-touching paths (enable / disable / linger) need a
// live user-systemd and are covered by the operator-side
// integration smoke documented in the runbook (Phase E).

// ----- discoverStacks ------------------------------------------------------

func TestDiscoverStacks_FindsAllComposeShapes(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "a"))
	mustMkdir(t, filepath.Join(root, "b"))
	mustMkdir(t, filepath.Join(root, "c"))
	mustMkdir(t, filepath.Join(root, "d"))
	mustWrite(t, filepath.Join(root, "a", "docker-compose.yml"), "")
	mustWrite(t, filepath.Join(root, "b", "docker-compose.yaml"), "")
	mustWrite(t, filepath.Join(root, "c", "compose.yml"), "")
	mustWrite(t, filepath.Join(root, "d", "compose.yaml"), "")

	stacks, err := discoverStacks(root, 5, nil)
	if err != nil {
		t.Fatalf("discoverStacks: %v", err)
	}
	if len(stacks) != 4 {
		t.Fatalf("got %d stacks, want 4: %+v", len(stacks), stacks)
	}
	// Output is sorted by ComposeDir; assert that ordering is
	// stable so the JSON we return to agents is reproducible.
	if !sort.SliceIsSorted(stacks, func(i, j int) bool {
		return stacks[i].ComposeDir < stacks[j].ComposeDir
	}) {
		t.Fatal("discoverStacks output not sorted")
	}
}

func TestDiscoverStacks_SkipsBuiltinDirs(t *testing.T) {
	root := t.TempDir()
	for _, junk := range []string{"node_modules", ".git", "vendor", "target", "dist", "__pycache__"} {
		mustMkdir(t, filepath.Join(root, junk, "deep"))
		mustWrite(t, filepath.Join(root, junk, "deep", "docker-compose.yml"), "")
	}
	mustMkdir(t, filepath.Join(root, "real-app"))
	mustWrite(t, filepath.Join(root, "real-app", "docker-compose.yml"), "")

	skip := map[string]struct{}{}
	for _, d := range defaultSkip {
		skip[d] = struct{}{}
	}
	stacks, err := discoverStacks(root, 5, skip)
	if err != nil {
		t.Fatalf("discoverStacks: %v", err)
	}
	if len(stacks) != 1 || filepath.Base(stacks[0].ComposeDir) != "real-app" {
		t.Fatalf("expected only real-app to survive skip; got %+v", stacks)
	}
}
