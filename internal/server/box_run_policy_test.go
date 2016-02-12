package server

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func hasRule(rules []string, r string) bool {
	for _, x := range rules {
		if x == r {
			return true
		}
	}
	return false
}

// Each level grants exactly its own tier, and nothing ever grants publishing
// (git push / remote), a bare Bash, or a wildcard.
func TestBoxRunPolicyFor_LevelsAreCumulativeAndBounded(t *testing.T) {
	none, err := boxRunPolicyFor("none")
	if err != nil {
		t.Fatal(err)
	}
	build, err := boxRunPolicyFor("build_tools")
	if err != nil {
		t.Fatal(err)
	}
	full, err := boxRunPolicyFor("full_sudo")
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range []string{"Read", "Edit", "Write", "Bash(git status)", "Bash(git commit *)", boxRunSkillFetchTool} {
		if !hasRule(none.Allowed, r) {
			t.Errorf("NONE lacks the base rule %q", r)
		}
	}
	if hasRule(none.Allowed, "Bash(go test *)") || hasRule(none.Allowed, "Bash(sudo *)") {
		t.Error("NONE must not allow build tools or sudo")
	}
	if !hasRule(build.Allowed, "Bash(go test *)") || !hasRule(build.Allowed, "Bash(make)") || !hasRule(build.Allowed, "Bash(pnpm install *)") {
		t.Error("BUILD_TOOLS must allow the toolchains")
	}
	if hasRule(build.Allowed, "Bash(sudo *)") {
		t.Error("BUILD_TOOLS must not allow sudo")
	}
	if !hasRule(full.Allowed, "Bash(sudo *)") || !hasRule(full.Allowed, "Bash(go test *)") {
		t.Error("FULL_SUDO must allow sudo and the toolchains")
	}
	if len(none.Allowed) >= len(build.Allowed) || len(build.Allowed) >= len(full.Allowed) {
		t.Errorf("levels must be cumulative: %d < %d < %d", len(none.Allowed), len(build.Allowed), len(full.Allowed))
	}
	for name, p := range map[string]boxRunPolicy{"none": none, "build": build, "full": full} {
		for _, r := range p.Allowed {
			switch {
			case r == "Bash", r == "*", strings.Contains(r, "git push"), strings.Contains(r, "git remote"),
				strings.Contains(r, "curl"), strings.Contains(r, "wget"), strings.Contains(r, "go run"), strings.Contains(r, "go install"):
				t.Errorf("%s allows %q, which no level may grant", name, r)
			}
		}
	}
}

// An unknown, empty or hand-edited level is an error, never a default policy.
func TestBoxRunPolicyFor_UnknownLevelFailsClosed(t *testing.T) {
	for _, level := range []string{"", "root", "FULL_SUDO", "install_packages", "full-sudo"} {
		if _, err := boxRunPolicyFor(level); err == nil {
			t.Errorf("level %q must have no policy", level)
		}
	}
}

// The flags survive a real shell as exactly the intended tokens: the rules
// contain spaces, '*' and parentheses, and an unquoted one would glob or break.
func TestBoxRunPolicy_CommandFlagsRoundTripThroughAShell(t *testing.T) {
	p, err := boxRunPolicyFor("build_tools")
	if err != nil {
		t.Fatal(err)
	}
	flags := p.commandFlags()
	out, err := exec.Command("sh", "-c", "for a in"+flags+"; do printf '%s\\n' \"$a\"; done").CombinedOutput()
	if err != nil {
		t.Fatalf("flags do not parse as shell: %v\n%s", err, out)
	}
	got := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	want := append([]string{"--restricted", "--tools", strings.Join(p.Tools, ","), "--allowedTools"}, p.Allowed...)
	if len(got) != len(want) {
		t.Fatalf("token count = %d, want %d\n%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token %d = %q, want %q", i, got[i], want[i])
		}
	}
	if got[0] != "--restricted" || got[len(got)-1] != p.Allowed[len(p.Allowed)-1] {
		t.Error("--restricted must come first and the variadic --allowedTools last")
	}
}

func TestBoxRunSudoersScript(t *testing.T) {
	for _, level := range []string{"none", "build_tools", "", "garbage"} {
		s := boxRunSudoersScript(level)
		if s != "rm -f '/etc/sudoers.d/90-ctn-run'\n" {
			t.Errorf("level %q must only REMOVE the grant, got:\n%s", level, s)
		}
	}
	s := boxRunSudoersScript("full_sudo")
	for _, want := range []string{"visudo -cf", "NOPASSWD:ALL", "chmod 0440", "mv -f", "command -v visudo", "90-ctn-run", "'ctn-run'"} {
		if !strings.Contains(s, want) {
			t.Errorf("full_sudo script lacks %q:\n%s", want, s)
		}
	}
	for _, sc := range []string{boxRunSudoersScript("full_sudo"), boxRunSudoersScript("none")} {
		if out, err := exec.Command("sh", "-n", "-c", sc).CombinedOutput(); err != nil {
			t.Errorf("sudoers script does not parse: %v\n%s", err, out)
		}
	}
}

type policyRun struct {
	rootCalls []string
	userCalls []string
	err       error
}

// runWithLevel runs StartBoxRun against recording fakes with the given recorded
// level and the owner's code.json engine config.
func runWithLevel(t *testing.T, level, engineJSON string) policyRun {
	t.Helper()
	var mu sync.Mutex
	var r policyRun
	runHome := "/srv/" + boxRunUnprivilegedUser
	s := &ContainerServer{
		boxRunLogDir:          t.TempDir(),
		boxRunCollaborator:    func(owner, subject string) (bool, error) { return false, nil },
		boxRunToolAccess:      func(ctx context.Context, username string) (string, error) { return level, nil },
		boxRunEnsureToolchain: func(string) error { return nil }, // mounted host release
		boxRunExec: func(box, script string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			r.rootCalls = append(r.rootCalls, script)
			if strings.Contains(script, "useradd") {
				return runHome + "\n", nil
			}
			return "", nil
		},
		boxRunStartExec: func(box, username, script string) (string, error) {
