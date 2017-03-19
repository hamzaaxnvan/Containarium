package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestClaudeProjectSlug pins the "/" -> "-" convention #2193's accepted
// default commits to (unverified against a live Claude Code install — see
// session_discovery.go's doc comment).
func TestClaudeProjectSlug(t *testing.T) {
	tests := []struct{ cwd, want string }{
		{"/home/alice", "-home-alice"},
		{"/home/alice/myproject", "-home-alice-myproject"},
		{"/", "-"},
	}
	for _, tc := range tests {
		if got := ClaudeProjectSlug(tc.cwd); got != tc.want {
			t.Errorf("ClaudeProjectSlug(%q) = %q, want %q", tc.cwd, got, tc.want)
		}
	}
}

// TestSessionDiscoveryBackgroundCommand_UnknownEngine pins that an engine
// with no discoverable session id (there are none today, but a future engine
// added to Name without updating sessionSearchDir must fail closed, not
// silently poll a made-up path).
func TestSessionDiscoveryBackgroundCommand_UnknownEngine(t *testing.T) {
	if _, ok := SessionDiscoveryBackgroundCommand(Name("made-up"), "/home/alice", "/home/alice", "/tmp/x.session"); ok {
		t.Error("an unknown engine should report ok=false, not a command")
	}
}

// discoveryFixture drives the real prepare/poll shell commands against a
// fake engine session directory, so the tests prove the mechanism against a
// real shell rather than matching on the generated string.
type discoveryFixture struct {
	t       *testing.T
	engine  Name
	home    string
	cwd     string
	dir     string
	suffix  string
	sidecar string
}

func newDiscoveryFixture(t *testing.T, n Name) *discoveryFixture {
	t.Helper()
	home := t.TempDir()
	f := &discoveryFixture{t: t, engine: n, home: home, cwd: filepath.Join(home, "myproject"), sidecar: filepath.Join(t.TempDir(), "run.session")}
	switch n {
	case NameClaude:
		f.dir = filepath.Join(home, ".claude", "projects", ClaudeProjectSlug(f.cwd))
		f.suffix = ".jsonl"
	default:
		f.dir = filepath.Join(home, ".pi", "agent", "sessions")
	}
	mustMkdirAll(t, f.dir)
	return f
}

func (f *discoveryFixture) sh(cmd string) {
	f.t.Helper()
	// #nosec G204 -- cmd is built entirely by the functions under test from
