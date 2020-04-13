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
