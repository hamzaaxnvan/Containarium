package runlease

import (
	"sync"
	"testing"
)

func TestRegistry_RegisterThenGet(t *testing.T) {
	r := NewRegistry()
	r.Register("run-1", Info{SkillID: "code-review", Model: "sonnet"})

	got, ok := r.Get("run-1")
	if !ok {
		t.Fatal("Get(run-1) = not found, want found")
	}
	if got.SkillID != "code-review" || got.Model != "sonnet" {
		t.Errorf("Get(run-1) = %+v, want {code-review sonnet}", got)
	}
}

// TestRegistry_InfoCarriesBoxGitCommitWorkspace is #1923's addition:
// SubmitTrackerChange needs to resolve a run's box, base commit,
// workspace path, and original ref from the same registry
// ClaimTrackerIssue already reads for liveness — a round trip through
// Register/Get must carry all four through unchanged, including the
// case where a run had no git_source at all (empty
// GitCommit/Workspace/GitRef, not a zero-value that looks like a
