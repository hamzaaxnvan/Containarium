package submit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #2798: PushSpec.Files are committed on top of the bundle's head, in the
// host-side bare repository, before the push, so the pushed branch carries
// them (the sealed Proof Pack).

// TestGitPusher_RealGit_CommitsFilesOnTopOfHead runs real git against a
// local bare remote: the pushed branch's tip is a new commit whose parent is
// the agent's head and whose tree has the file, replacing an agent-planted
// file at the same path.
func TestGitPusher_RealGit_CommitsFilesOnTopOfHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	originDir := filepath.Join(root, "origin.git")
	workDir := filepath.Join(root, "work")
	if err := os.MkdirAll(filepath.Join(workDir, ".containarium"), 0o755); err != nil {
		t.Fatal(err)
	}
