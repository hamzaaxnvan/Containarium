package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/coderun/engine"
	"github.com/footprintai/containarium/internal/connectcore"
	"github.com/footprintai/containarium/pkg/core/sdlcskills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// mustEngine builds an engine or fails the test, for table entries where the
// two-value form would be unreadable.
func mustEngine(t *testing.T, name engine.Name, cred engine.CredentialSource) engine.Engine {
	t.Helper()
	e, err := engine.For(name, engine.Options{Credential: cred})
	if err != nil {
		t.Fatalf("engine.For(%q): %v", name, err)
	}
	return e
}

func TestShellQuoteSingle_RoundTripsThroughARealShell(t *testing.T) {
	cases := []string{
		"plain text",
		"it's got an apostrophe",
		"multiple ''' quotes '' in a row",
		"",
		"$(rm -rf /) and `also this`",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			quoted := coderun.ShellQuoteSingle(in)
			// #nosec G204 -- fixed "sh -c" with a single literal-quoted
			// argument built by the function under test; this is the
			// injection check itself, not a caller-reachable path.
			out, err := exec.Command("/bin/sh", "-c", "printf '%s' "+quoted).Output()
			if err != nil {
				t.Fatalf("shell rejected %q (quoted as %q): %v", in, quoted, err)
			}
			if string(out) != in {
				t.Errorf("round-trip = %q, want %q (quoted form was %q)", out, in, quoted)
			}
		})
	}
}

func TestShellQuoteSingle_NeverEscapesOutOfTheQuotedString(t *testing.T) {
	// The classic injection shape: a naive quoter that doesn't handle
	// embedded quotes lets this argument terminate early and run a second
	// command.
	malicious := "'; touch /tmp/pwned; echo '"
	quoted := coderun.ShellQuoteSingle(malicious)
	// #nosec G204 -- see above.
	out, err := exec.Command("/bin/sh", "-c", "echo "+quoted).Output()
	if err != nil {
		t.Fatalf("shell rejected quoted input: %v", err)
	}
	got := strings.TrimSuffix(string(out), "\n")
	if got != malicious {
		t.Errorf("got %q, want the argument echoed back verbatim (%q) — the shell ran something other than plain echo", got, malicious)
	}
}

func TestBuildClaudeRunCommand(t *testing.T) {
	cmd := coderun.BuildClaudeRunCommand("fix the bug", false)
	for _, want := range []string{"secrets.env", "~/.local/bin/claude", "-p", coderun.ShellQuoteSingle("fix the bug")} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command missing %q:\n%s", want, cmd)
		}
	}
	if strings.Contains(cmd, "stream-json") {
		t.Errorf("combined-mode command should not request stream-json:\n%s", cmd)
	}
}

func TestBuildClaudeRunCommand_StreamJSON(t *testing.T) {
	cmd := coderun.BuildClaudeRunCommand("fix the bug", true)
	if !strings.Contains(cmd, "--output-format stream-json") {
		t.Errorf("expected --output-format stream-json in:\n%s", cmd)
	}
}

func TestRunOutcomeLine(t *testing.T) {
	listing := "Found 2 process(es):\n\n" +
		"🟢 code  (pid 111, running)\n" +
		"   Command:    sleep 5\n" +
		"   Started at: 2026-09-02T12:00:00Z\n" +
		"   Log path:   /tmp/agent-box/code.log\n\n" +
		"⚪ old-task  (pid 222, exited)\n" +
		"   Command:    true\n" +
		"   Exit code:  0\n" +
		"   Log path:   /tmp/agent-box/old-task.log\n\n"

	line, running := coderun.RunOutcomeLine(listing, "code")
	if !running {
		t.Errorf("code should be reported running, line=%q", line)
	}
	if !strings.Contains(line, "code") || !strings.Contains(line, "running") {
		t.Errorf("line = %q", line)
	}

	line, running = coderun.RunOutcomeLine(listing, "old-task")
	if running {
		t.Errorf("old-task should not be reported running, line=%q", line)
	}
	if !strings.Contains(line, "old-task") {
		t.Errorf("line = %q", line)
	}

	line, running = coderun.RunOutcomeLine(listing, "does-not-exist")
	if running || line != "" {
		t.Errorf("unknown name should report not-running/empty line, got running=%v line=%q", running, line)
	}
}

// TestCodeRunExitErr is the #2011 acceptance test for `code run`/`code
// attach`'s process status: streamAndWait used to return nil once a run
// stopped, no matter what the agent's own exit code was, so a failing agent
// looked identical to a passing one to a caller checking $?. A listing
// carrying both an exited-0 and an exited-1 run must turn only the
// non-zero one into a non-nil error.
func TestCodeRunExitErr(t *testing.T) {
	listing := "Found 3 process(es):\n\n" +
		"⚪ ok-run  (pid 111, exited)\n" +
		"   Command:    true\n" +
		"   Exit code:  0\n" +
		"   Log path:   /tmp/agent-box/ok-run.log\n\n" +
		"⚪ bad-run  (pid 222, exited)\n" +
		"   Command:    false\n" +
		"   Exit code:  1\n" +
		"   Log path:   /tmp/agent-box/bad-run.log\n\n" +
		"⚪ unknown-run  (pid 333, exited)\n" +
		"   Command:    kill -9 $$\n" +
		"   Log path:   /tmp/agent-box/unknown-run.log\n\n"

	if err := codeRunExitErr(listing, "ok-run"); err != nil {
		t.Errorf("a zero exit code must not error (`code run` should exit 0), got %v", err)
	}
	if err := codeRunExitErr(listing, "bad-run"); err == nil {
		t.Error("a non-zero exit code must return a non-nil error so `code run` exits non-zero")
	}
	if err := codeRunExitErr(listing, "unknown-run"); err != nil {
		t.Errorf("a run with no recorded exit code has nothing to gate on and must not error, got %v", err)
	}
}

func TestLogPathFromListing(t *testing.T) {
