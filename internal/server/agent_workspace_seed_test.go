package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBuildWorkspaceSeedScript pins the daemon side of the workspace contract
// (design doc §5, coding-skill-on-a-repo, cloud repo): <seed>/workspace.json
// carries exactly the fields a runtime needs to bind its file tools to the
// checkout without re-deriving the path from the run id — path, and the repo
// coordinates the response already reports (git_source/git_ref/git_commit).
func TestBuildWorkspaceSeedScript(t *testing.T) {
	seedDir := seedDirFor("run-git-1")
	script := buildWorkspaceSeedScript(seedDir, workspaceSeed{
		Path:      workspaceDirFor("run-git-1"),
		GitSource: "https://github.com/octocat/Spoon-Knife",
		GitRef:    "d0dd1f61b33d64e29d8bc1372a94ef6a2fee76a9",
		GitCommit: "d0dd1f61b33d64e29d8bc1372a94ef6a2fee76a9",
	})

	if !strings.Contains(script, "mkdir -p "+seedDir) {
		t.Errorf("script does not ensure the seed dir exists:\n%s", script)
	}
	if !strings.Contains(script, seedDir+"/workspace.json") {
		t.Errorf("script does not write workspace.json into the seed dir:\n%s", script)
	}

	// The JSON payload itself, single-quoted for printf, must decode back to
	// exactly what was passed in — a stale/hand-rolled script would drift
	// silently on a field rename, this catches it.
	payload := extractSingleQuotedPrintfArg(t, script)
	var got workspaceSeed
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("workspace.json payload is not valid JSON: %v\npayload: %s", err, payload)
	}
	want := workspaceSeed{
		Path:      "/workspace/runs/run-git-1",
