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
	listing := "Found 1 process(es):\n\n" +
		"🟢 code  (pid 111, running)\n" +
		"   Command:    claude -p hi\n" +
		"   Started at: 2026-09-02T12:00:00Z\n" +
		"   Log path:   /tmp/agent-box/code.log\n\n"

	got, err := coderun.LogPathFromListing(listing, "code")
	if err != nil {
		t.Fatalf("logPathFromListing: %v", err)
	}
	if got != "/tmp/agent-box/code.log" {
		t.Errorf("got %q, want /tmp/agent-box/code.log", got)
	}

	if _, err := coderun.LogPathFromListing(listing, "nope"); err == nil {
		t.Error("expected an error for a name not in the listing")
	}
}

// --- `code install` (#2030) ------------------------------------------------
//
// #2030 removed the CLAUDE_CODE_OAUTH_TOKEN tenant-secret precheck: a platform
// may not collect, store, or intermediate a Claude.ai credential
// (https://code.claude.com/docs/en/legal-and-compliance). The install lands a
// toolchain and nothing else; the user signs in inside the box, or places
// their own API key. These tests drive runCodeInstall through the sshExec /
// resolveCodeTargetFn seams, so no box, daemon, or ssh binary is involved.

// installRun is one captured remote exec: the script runCodeInstall asked the
// box to run.
type installRun struct {
	script string
}

// runInstall drives runCodeInstall against fakes and returns every remote
// script it ran, plus the command's own stdout.
func runInstall(t *testing.T, box string, flags map[string]string) ([]installRun, string, error) {
	t.Helper()

	origSSH, origResolve := sshExec, resolveCodeTargetFn
	origBootstrap, origRelease, origVersion := codeBootstrapURL, codeRelease, codeClaudeCodeVersion
	t.Cleanup(func() {
		sshExec, resolveCodeTargetFn = origSSH, origResolve
		codeBootstrapURL, codeRelease, codeClaudeCodeVersion = origBootstrap, origRelease, origVersion
	})

	codeBootstrapURL, codeRelease, codeClaudeCodeVersion = flags["bootstrap-url"], flags["release"], flags["claude-code-version"]

	var runs []installRun
	resolveCodeTargetFn = func(context.Context, string, io.Writer) (connectcore.Target, string, error) {
		return connectcore.Target{User: "alice", Host: "box.example.test", Port: 22}, "/dev/null", nil
	}
	sshExec = func(_ io.Writer, args []string) (string, error) {
		script := ""
		if len(args) > 0 {
			script = args[len(args)-1]
		}
		runs = append(runs, installRun{script: script})
		return "1.2.3 (Claude Code)\ncredential sources present:\n  (none)", nil
	}

	cmd := &cobra.Command{}
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetContext(context.Background())
	err := runCodeInstall(cmd, []string{box})
	return runs, out.String(), err
}

func allScripts(runs []installRun) string {
	var sb strings.Builder
	for _, r := range runs {
		sb.WriteString(r.script)
		sb.WriteString("\n")
	}
	return sb.String()
}

// TestCodeInstall_NoCredentialPrecheck is the headline #2030 AC: "Remove the
// CLAUDE_CODE_OAUTH_TOKEN tenant-secret precheck. `code install` neither
// reads, lists, nor names a Claude.ai token." It also pins that --server is no
// longer required, since the only reason it was is gone.
func TestCodeInstall_NoCredentialPrecheck(t *testing.T) {
	origServer := serverAddr
	serverAddr = "" // used to be a hard error: "--server is required"
	t.Cleanup(func() { serverAddr = origServer })

	runs, _, err := runInstall(t, "alice", nil)
	if err != nil {
		t.Fatalf("install failed with no --server and no credential set: %v", err)
	}
	if len(runs) == 0 {
		t.Fatal("no remote command ran")
	}

	scripts := allScripts(runs)
	banned := []string{
		"CLAUDE_CODE_OAUTH_TOKEN",
		"setup-token",
		"secrets set",
		"/run/secrets/",
	}
	for _, b := range banned {
		if strings.Contains(scripts, b) {
			t.Errorf("install scripts still reference %q:\n%s", b, scripts)
		}
	}
	// The same promise on the surface the user reads.
	help := codeInstallCmd.Long + "\n" + codeInstallCmd.Short
	for _, b := range banned {
		if strings.Contains(help, b) {
			t.Errorf("`code install` help still tells users about %q", b)
		}
	}
	// Anthropic's own installer line must be unchanged.
	if !strings.Contains(scripts, "curl -fsSL https://claude.ai/install.sh | bash") {
		t.Errorf("Anthropic's installer line is not run verbatim:\n%s", scripts)
	}
}

// TestCodeInstall_VerifyScript covers the replaced verification: `claude
// --version`, an assertion that the install created no ~/.claude/.credentials.json,
// and a report of which credential SOURCE is present — names only, never a
// value.
func TestCodeInstall_VerifyScript(t *testing.T) {
	script := claudeVerifyScript()

	tests := []struct {
		name   string
		want   string
		absent bool
	}{
		{name: "runs the installed binary's version", want: "/.local/bin/claude"},
		{name: "version, not a prompt", want: "--version"},
		{name: "no non-interactive prompt any more", want: "claude -p ", absent: true},
		{name: "asserts no credentials file was created", want: ".credentials.json"},
		{name: "names the API-key source", want: "ANTHROPIC_API_KEY"},
		{name: "names the auth-token source", want: "ANTHROPIC_AUTH_TOKEN"},
		{name: "names the settings.json env block", want: "settings.json"},
		{name: "names 3P inference providers", want: "CLAUDE_CODE_USE_"},
		// Presence only. Expanding any of these would put a live credential
		// into the CLI's own output and the user's scrollback.
		{name: "never expands the API key", want: `$ANTHROPIC_API_KEY`, absent: true},
		{name: "never expands the auth token", want: `$ANTHROPIC_AUTH_TOKEN`, absent: true},
		{name: "never cats the credentials file", want: "cat \"$HOME/.claude/.credentials.json\"", absent: true},
		{name: "never cats settings.json", want: "cat \"$settings\"", absent: true},
		// #1673's own rule, still true: bare mode is never used here.
		{name: "no bare mode", want: "--bare", absent: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Contains(script, tc.want)
			if tc.absent && got {
				t.Errorf("verify script must NOT contain %q:\n%s", tc.want, script)
			}
			if !tc.absent && !got {
				t.Errorf("verify script is missing %q:\n%s", tc.want, script)
