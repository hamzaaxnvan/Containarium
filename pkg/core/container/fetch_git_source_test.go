package container

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// fakeExecBackend is a minimal incus.Backend that only implements
// ExecWithOutput, calling it directly via FetchGitSource (not through
// (*Manager).ExecWithOutput, which type-asserts to the concrete *incus.Client
// and so cannot be exercised against any fake). Every other Backend method is
// unimplemented on purpose — FetchGitSource must never call them.
type fakeExecBackend struct {
	incus.Backend
	gotContainer string
	gotCommand   []string
	stdout       string
	stderr       string
	err          error
}

func (b *fakeExecBackend) ExecWithOutput(containerName string, command []string) (string, string, error) {
	b.gotContainer = containerName
	b.gotCommand = command
	return b.stdout, b.stderr, b.err
}

func TestFetchGitSource_ReturnsResolvedCommit(t *testing.T) {
	backend := &fakeExecBackend{stdout: "some git output\nmore output\ndeadbeefcafe1234\n"}
	mgr := NewWithBackend(backend)

	commit, err := mgr.FetchGitSource("agent-code-review-container", GitSourceSpec{
		Source: "https://github.com/org/repo",
		Ref:    "main",
	})
	if err != nil {
		t.Fatalf("FetchGitSource: %v", err)
	}
	if commit != "deadbeefcafe1234" {
		t.Errorf("commit = %q, want the script's last stdout line", commit)
	}
	if backend.gotContainer != "agent-code-review-container" {
		t.Errorf("exec ran against %q, want the container name", backend.gotContainer)
	}
	if len(backend.gotCommand) != 3 || backend.gotCommand[0] != "/bin/sh" || backend.gotCommand[1] != "-c" {
		t.Errorf("command = %v, want [/bin/sh -c <script>]", backend.gotCommand)
	}
	script := backend.gotCommand[2]
	if !strings.Contains(script, "git rev-parse HEAD") {
		t.Errorf("script must print the resolved commit as its last line:\n%s", script)
	}
}

func TestFetchGitSource_ErrorSurfacesStderrNotCredential(t *testing.T) {
	backend := &fakeExecBackend{
		stderr: "fatal: could not read Username",
		err:    errTest,
	}
	mgr := NewWithBackend(backend)

