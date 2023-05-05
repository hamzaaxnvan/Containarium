package submit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testGit runs a real git command for test-fixture setup only — not the
// package under test's own hardened path, just plain git so the fixture
// looks like an ordinary repository a developer or agent would produce.
func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) // #nosec G204 -- test-only, fixed argv, test tmp dirs.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s (dir=%s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestGitPusher_RealGit_PushesToLocalBareRemote is an integration test
// (real git, no network — the "remote" is a local bare repository) that
// PushBundle's actual git plumbing, not just its argv/env shape, produces
// a correct result: given a bundle of one new commit on top of a base
// already present on the remote, the remote ends up with a new branch at
// exactly that commit, non-destructively (nothing else on the remote is
