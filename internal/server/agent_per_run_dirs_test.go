package server

import (
	"strings"
	"testing"
)

// TestSeedDirFor_AndWorkspaceDirFor_ArePerRun pins the property #1860 exists
// for: two different run ids of the same skill get two different, non-
// colliding directories. provisionSkillBox's own happy path cannot be driven
// end-to-end in a unit test (its seed exec always fails on every fake
// backend — see newSkillBoxHarness's doc comment), so this is the closest
// unit-testable proxy for "two concurrent runs get distinct directories";
// the full integration is the e2e's job (#1861).
func TestSeedDirFor_AndWorkspaceDirFor_ArePerRun(t *testing.T) {
	a, b := seedDirFor("run-a"), seedDirFor("run-b")
	if a == b {
		t.Fatalf("two different run ids produced the same seed dir: %q", a)
	}
	if !strings.HasSuffix(a, "/run-a") || !strings.HasSuffix(b, "/run-b") {
		t.Errorf("seed dir must end in the run id, got %q / %q", a, b)
	}
	if !strings.HasPrefix(a, agentSeedRoot+"/") || !strings.HasPrefix(b, agentSeedRoot+"/") {
		t.Errorf("seed dir must live under agentSeedRoot %q, got %q / %q", agentSeedRoot, a, b)
	}
