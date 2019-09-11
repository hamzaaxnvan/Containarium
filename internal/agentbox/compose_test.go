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

func TestDiscoverStacks_NoSkipFindsEverything(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "node_modules"))
	mustWrite(t, filepath.Join(root, "node_modules", "docker-compose.yml"), "")
	mustMkdir(t, filepath.Join(root, "app"))
	mustWrite(t, filepath.Join(root, "app", "docker-compose.yml"), "")

	stacks, err := discoverStacks(root, 5, nil) // empty skip = walk everything
	if err != nil {
		t.Fatalf("discoverStacks: %v", err)
	}
	if len(stacks) != 2 {
		t.Fatalf("expected 2 stacks (including node_modules), got %d", len(stacks))
	}
}

func TestDiscoverStacks_HonorsMaxDepth(t *testing.T) {
	root := t.TempDir()
	// depth 3 below root: root/a/b/c/compose.yml
	deep := filepath.Join(root, "a", "b", "c")
	mustMkdir(t, deep)
	mustWrite(t, filepath.Join(deep, "compose.yml"), "")
	// depth 1: root/x/compose.yml
	mustMkdir(t, filepath.Join(root, "x"))
	mustWrite(t, filepath.Join(root, "x", "compose.yml"), "")

	// maxDepth=1 → only ./x should match.
	stacks, err := discoverStacks(root, 1, nil)
	if err != nil {
		t.Fatalf("discoverStacks: %v", err)
	}
	if len(stacks) != 1 || filepath.Base(stacks[0].ComposeDir) != "x" {
		t.Fatalf("maxDepth=1 should find only ./x; got %+v", stacks)
	}

	// maxDepth=5 → both.
	stacks, _ = discoverStacks(root, 5, nil)
	if len(stacks) != 2 {
		t.Fatalf("maxDepth=5 should find both; got %d", len(stacks))
	}
}

func TestDiscoverStacks_ComposeYmlBeatsDockerComposeYml(t *testing.T) {
	// Both file shapes in one dir — `compose.yml` is the
	// modern name and wins, matching docker compose v2's
	// own resolution order.
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "p"))
	mustWrite(t, filepath.Join(root, "p", "docker-compose.yml"), "")
	mustWrite(t, filepath.Join(root, "p", "compose.yml"), "")

	stacks, _ := discoverStacks(root, 5, nil)
	if len(stacks) != 1 {
		t.Fatalf("expected 1 stack, got %d", len(stacks))
	}
	if filepath.Base(stacks[0].ComposeFile) != "compose.yml" {
		t.Errorf("expected compose.yml to win; got %s", stacks[0].ComposeFile)
	}
}

// ----- stackSlug / sanitizeSlug -------------------------------------------

func TestStackSlug_UsesTwoTailComponentsToReduceCollisions(t *testing.T) {
	cases := map[string]string{
		"/home/alice/deploy":              "alice-deploy",
		"/home/alice/Containarium/deploy": "containarium-deploy",
		"/home/bob/api":                   "bob-api",
