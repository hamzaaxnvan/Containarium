package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/sdlcskills"
)

// `code install` installs the pinned SDLC role skills for the Claude engine
// (#2065), the same as the coding-agent recipe does for a new box.

func withNoSDLCSkills(t *testing.T, v bool) {
	t.Helper()
	orig, origSets := codeNoSDLCSkills, codeSkillSets
	t.Cleanup(func() { codeNoSDLCSkills, codeSkillSets = orig, origSets })
	codeNoSDLCSkills, codeSkillSets = v, ""
}

func withSkillSets(t *testing.T, sets string, noSDLC bool) {
	t.Helper()
	withNoSDLCSkills(t, noSDLC)
	codeSkillSets = sets
}

func TestCodeInstall_InstallsSDLCSkillsForClaude(t *testing.T) {
	withNoSDLCSkills(t, false)
	runs, _, err := runInstall(t, "alice", map[string]string{"release": "v9.9.9"})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	scripts := allScripts(runs)
	for _, want := range []string{"install-sdlc-skills.sh", "/v9.9.9/scripts/install-sdlc-skills.sh"} {
		if !strings.Contains(scripts, want) {
			t.Errorf("the SDLC skills step is missing %q:\n%s", want, scripts)
		}
	}
	if strings.Contains(scripts, "sudo") {
		t.Errorf("skills belong to the box user; no sudo:\n%s", scripts)
	}
}

// --no-sdlc-skills is a deprecated alias for --skill-sets none (#3048): the
// installer still runs, with SKILL_SETS=none, so skills it owned are removed.
func TestCodeInstall_NoSDLCSkillsFlagIsAnAliasForNone(t *testing.T) {
	withNoSDLCSkills(t, true)
	runs, _, err := runInstall(t, "alice", map[string]string{"release": "v9.9.9"})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if scripts := allScripts(runs); !strings.Contains(scripts, `SKILL_SETS="none"`) {
		t.Errorf("--no-sdlc-skills did not map to SKILL_SETS=none:\n%s", scripts)
	}
	f := codeInstallCmd.Flags().Lookup("no-sdlc-skills")
	if f == nil {
		t.Fatal("code install has no --no-sdlc-skills flag")
	}
	if !strings.Contains(f.Deprecated, "--skill-sets none") {
		t.Errorf("--no-sdlc-skills deprecation note = %q, want it to name --skill-sets none", f.Deprecated)
	}
}

func TestCodeInstall_SkillSetsFlagSelectsSets(t *testing.T) {
	for in, want := range map[string]string{"": `SKILL_SETS="all"`, "qa": `SKILL_SETS="qa"`, "sdlc, qa": `SKILL_SETS="sdlc,qa"`, "none": `SKILL_SETS="none"`} {
		withSkillSets(t, in, false)
		runs, _, err := runInstall(t, "alice", map[string]string{"release": "v9.9.9"})
		if err != nil {
			t.Fatalf("--skill-sets %q: %v", in, err)
		}
		if scripts := allScripts(runs); !strings.Contains(scripts, want) {
			t.Errorf("--skill-sets %q: script lacks %s:\n%s", in, want, scripts)
		}
	}
	if f := codeInstallCmd.Flags().Lookup("skill-sets"); f == nil {
		t.Error("code install has no --skill-sets flag")
	}
}

func TestCodeInstall_SkillSetsAreValidatedBeforeAnythingRuns(t *testing.T) {
	for _, tc := range []struct {
		sets   string
		noSDLC bool
	}{{"qa;touch x", false}, {"$(id)", false}, {"QA", false}, {"qa,none", false}, {"qa", true}} {
		withSkillSets(t, tc.sets, tc.noSDLC)
		runs, _, err := runInstall(t, "alice", map[string]string{"release": "v9.9.9"})
		if err == nil {
			t.Errorf("--skill-sets %q (no-sdlc-skills=%v) accepted", tc.sets, tc.noSDLC)
		}
		if len(runs) != 0 {
			t.Errorf("--skill-sets %q: %d scripts ran before the flag was rejected", tc.sets, len(runs))
		}
	}
	// --no-sdlc-skills together with --skill-sets none is consistent, not a conflict.
	withSkillSets(t, "none", true)
	if _, _, err := runInstall(t, "alice", map[string]string{"release": "v9.9.9"}); err != nil {
		t.Errorf("--no-sdlc-skills with --skill-sets none: %v", err)
	}
}

// The skills are Claude Code skills (~/.claude/skills); another engine does
// not read them.
func TestCodeInstall_OtherEnginesDoNotGetClaudeSkills(t *testing.T) {
	withNoSDLCSkills(t, false)
	env, err := runInstallEngine(t, "alice", map[string]string{
