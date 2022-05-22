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
