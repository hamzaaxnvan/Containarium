package stacks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the package directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("could not locate repo root from the package directory")
	return ""
}

// TestOnlyOneStackCatalogInRepo prevents the drift in #1131 from recurring.
//
// The repo previously carried two catalogs: this embedded one and
// configs/stacks.yaml. Only the embedded copy was maintained, so the other fell 5
// stacks behind and lost every rhel_packages/rhel_pre_install/rhel_post_install
// override — and because DefaultConfigPaths preferred the repo-relative path, a
// CLI run from a checkout silently used the stale one and installed Debian package
// names on RHEL. Nothing failed loudly; the two files simply disagreed.
//
// A second committed stacks.yaml is therefore a defect in itself, regardless of
// its contents.
func TestOnlyOneStackCatalogInRepo(t *testing.T) {
	root := repoRoot(t)
	var found []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable dir; not what this test is about
		}
		if d.IsDir() {
			base := d.Name()
			// Skip VCS, deps, and agent worktrees — those legitimately hold copies.
			if base == ".git" || base == "vendor" || base == "node_modules" || base == ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "stacks.yaml" {
			rel, _ := filepath.Rel(root, path)
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
