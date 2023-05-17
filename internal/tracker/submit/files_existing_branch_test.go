package submit

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #3288: when head_branch already exists on the remote, PushFiles builds
// the new commit on the head branch's tip and pushes it as a fast-forward;
// it never forces, and a tip that moved under it is refused.

// seedRemote creates a bare remote with one commit on "trunk" and returns
// the remote path, a work tree pushing to it, and the base commit.
func seedRemote(t *testing.T) (root, origin, work, baseSHA string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root = t.TempDir()
	origin = filepath.Join(root, "origin.git")
	work = filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "init", "--bare", "-q", origin)
	testGit(t, work, "init", "-q", "-b", "trunk")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, work, "add", ".")
	testGit(t, work, "commit", "-q", "-m", "base")
	testGit(t, work, "push", "-q", origin, "trunk")
	return root, origin, work, testGit(t, work, "rev-parse", "HEAD")
}

func filesSpec(origin string, files ...ChangeFile) FilesSpec {
	return FilesSpec{
		RemoteURL: origin, Credential: "unused-for-local-file-remote",
		BaseBranch: "trunk", HeadBranch: "containarium/issue-7",
		Message: "Fix the thing\n\nsigned", Files: files,
	}
