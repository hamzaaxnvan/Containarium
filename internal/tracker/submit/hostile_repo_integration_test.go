package submit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// localBox implements BoxRunner by running the script for real, locally
// — standing in for "exec inside the box" without needing incus. Good
// enough for this test's purpose: proving ExtractBundle's script, run
// against a REAL (hostile) git repository, behaves the same way it
// would inside a box. Every path ExtractBundle's script touches is
// already absolute (the workspace dir, the fixed remote bundle path),
// so this type needs no root of its own.
type localBox struct{}

func (b *localBox) ExecWithOutput(_ string, command []string) (string, string, error) {
	// command is ["/bin/sh", "-c", script] — run it for real.
	// #nosec G204 -- test-only, command built by this package's own code
	// under test, not from untrusted input reaching this test binary.
	cmd := exec.Command(command[0], command[1:]...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func (b *localBox) ReadFile(_ string, path string) ([]byte, error) {
	return os.ReadFile(path)
}

// TestSubmit_HostileRepo is the integration test named in the design
// note's test strategy: a workspace whose .git/config and hooks try to
// capture the push credential or redirect the push elsewhere. It
// exercises ExtractBundle and GitPusher.PushBundle TOGETHER, real git,
// no network (the tracker's "remote" is a local bare repository).
//
// The hooks/config below are deliberately the ones the design doc calls
// out by name: pre-push, credential.helper, url.<x>.insteadOf,
// http.proxy. None of them should ever fire, for two independent
// reasons this test checks separately:
//   - git bundle create (what ExtractBundle runs INSIDE the hostile
//     workspace) does no network I/O and does not invoke push hooks —
//     hostile config there has nothing to attach to.
//   - GitPusher.PushBundle never runs a single git command inside the
//     hostile workspace's .git directory at all — it builds its own
//     fresh bare repository elsewhere with GIT_CONFIG_NOSYSTEM=1,
//     GIT_CONFIG_GLOBAL=/dev/null, and its own HOME, so the hostile
//     workspace's config is never even on a path git would consult.
func TestSubmit_HostileRepo(t *testing.T) {
