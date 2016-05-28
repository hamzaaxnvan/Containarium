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
	testGit(t, root, "init", "--bare", "-q", originDir)
	testGit(t, workDir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, workDir, "add", "README.md")
	testGit(t, workDir, "commit", "-q", "-m", "base")
	baseSHA := testGit(t, workDir, "rev-parse", "HEAD")
	testGit(t, workDir, "remote", "add", "origin", originDir)
	testGit(t, workDir, "push", "-q", "origin", "main")
	// The agent's change plants its own file at the attach path.
	if err := os.WriteFile(filepath.Join(workDir, ".containarium", "proof-pack.json"), []byte("forged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, workDir, "add", ".")
	testGit(t, workDir, "commit", "-q", "-m", "agent change")
	headSHA := testGit(t, workDir, "rev-parse", "HEAD")
	bundlePath := filepath.Join(root, "out.bundle")
	testGit(t, workDir, "bundle", "create", bundlePath, baseSHA+"..HEAD")

	result, err := NewGitPusher().PushBundle(context.Background(), PushSpec{
		BundlePath: bundlePath, BaseSHA: baseSHA, HeadSHA: headSHA, RemoteURL: originDir,
		Credential: "unused", RunID: "run-files-2798-0001", IssueNumber: 9, Title: "Files",
		Files:        []File{{Path: ".containarium/proof-pack.json", Content: []byte("{\"sealed\":true}\n")}},
		FilesMessage: "Add Proof Pack",
	})
	if err != nil {
		t.Fatalf("PushBundle: %v", err)
	}
	if result.SHA == headSHA || len(result.SHA) != 40 {
